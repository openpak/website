//go:build e2e

package erasure

// End to end against the real services: the account core, saves and chat
// running as their own binaries on a throwaway Postgres, and the website
// store. A closed account is untouched while its grace period runs; once it is
// over the finaliser empties every service, and the core then answers as for
// an account that never existed.
//
// Run with -tags e2e and:
//
//	E2E_CORE_GRPC, E2E_CORE_HTTP, E2E_CORE_KEY, E2E_CORE_DB
//	E2E_SAVES_URL, E2E_SAVES_KEY, E2E_SAVES_DB
//	E2E_CHAT_URL,  E2E_CHAT_KEY,  E2E_CHAT_DB
//	WEBSITE_TEST_DATABASE_URL

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	accountv1 "github.com/openpak/website/internal/accountpb"
	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/chat"
	"github.com/openpak/website/internal/core"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/saves"
	"github.com/openpak/website/internal/store"
)

func need(t *testing.T, k string) string {
	t.Helper()
	v := os.Getenv(k)
	if v == "" {
		t.Skipf("%s not set", k)
	}
	return v
}

func count(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestEraseEverywhereAfterGrace(t *testing.T) {
	ctx := context.Background()
	key := need(t, "E2E_CORE_KEY")
	coreGRPC, err := core.DialGRPC(need(t, "E2E_CORE_GRPC"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer coreGRPC.Close()
	coreHTTP, err := core.NewHTTP(need(t, "E2E_CORE_HTTP"), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	open := func(env string) *pgxpool.Pool {
		p, err := pgxpool.New(ctx, need(t, env))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	coreDB, savesDB, chatDB := open("E2E_CORE_DB"), open("E2E_SAVES_DB"), open("E2E_CHAT_DB")
	savesCli := saves.New(need(t, "E2E_SAVES_URL"), need(t, "E2E_SAVES_KEY"))
	chatCli := chat.New(need(t, "E2E_CHAT_URL"), need(t, "E2E_CHAT_KEY"))
	db, err := store.New(ctx, need(t, "WEBSITE_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	files, _ := attachments.New(filepath.Join(dir, "att"))
	mst, _ := media.NewFSStorage(filepath.Join(dir, "media"))
	avatars := media.New(mst)

	// Two accounts, friends, through the core's own registration.
	conn, err := grpc.NewClient(need(t, "E2E_CORE_GRPC"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rctx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+key)
	suffix := fmt.Sprint(time.Now().UnixNano() % 1e9)
	register := func(name string) (string, string) {
		email := strings.ToLower(name) + suffix + "@example.org"
		resp, err := accountv1.NewRegistrationClient(conn).RegisterAccount(rctx, &accountv1.RegisterAccountRequest{
			CallerNamespace: "e2e", Purpose: "registration", Email: email, Password: "correct horse battery",
			Handle: name + suffix[:6]})
		if err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
		return resp.GetAccountId(), email
	}
	gone, goneEmail := register("Leaver")
	friend, _ := register("Friend")
	if err := coreGRPC.RequestFriend(ctx, gone, friend); err != nil {
		t.Fatal(err)
	}
	if err := coreGRPC.AcceptFriend(ctx, friend, gone); err != nil {
		t.Fatal(err)
	}

	// Data in every service.
	for _, m := range [][2]string{{gone, friend}, {friend, gone}} {
		if _, err := chatCli.Send(ctx, m[0], m[1], "hello", ""); err != nil {
			t.Fatalf("chat send: %v", err)
		}
	}
	var saveID int64
	if err := savesDB.QueryRow(ctx, `INSERT INTO saves (account_id, platform, title_id) VALUES ($1, 'switch', '0100aaaa') RETURNING id`, gone).Scan(&saveID); err != nil {
		t.Fatal(err)
	}
	if _, err := savesDB.Exec(ctx, `INSERT INTO versions (save_id, number, status, backend) VALUES ($1, 1, 'live', 'local')`, saveID); err != nil {
		t.Fatal(err)
	}
	if _, err := savesDB.Exec(ctx, `INSERT INTO storage_settings (account_id, kind, config) VALUES ($1, 's3', '\x00')`, gone); err != nil {
		t.Fatal(err)
	}
	if err := db.SavePushToken(ctx, gone, "e2e-push-"+suffix, "fcm"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMedia(ctx, store.MediaRecord{AccountID: gone, Kind: "avatar", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := avatars.StorePreset(ctx, gone, media.PresetForAccount(gone)); err != nil {
		t.Fatal(err)
	}
	att, err := files.Save("tickets", strings.NewReader("log"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTicket(ctx, store.Ticket{AccountID: gone, Subject: "help", Category: "other"},
		store.TicketMessage{AuthorID: gone, Body: "it broke", Attachment: store.Attachment{Key: att.Key, Name: "log.txt"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateCrash(ctx, store.CrashReport{Source: "app", AccountID: gone, Message: "boom", Fields: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}

	f := &Finaliser{Core: coreGRPC, Lock: db, Log: quiet(), Steps: []Step{
		{Name: "saves", Erase: savesCli.WipeAccount},
		{Name: "chat", Erase: chatCli.EraseAccount},
		WebsiteStep(db, files, avatars, quiet()),
	}}

	// Closed: nothing is erased while the grace period runs.
	if err := coreGRPC.RequestDeletion(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if res, err := f.RunOnce(ctx); err != nil || res.Due != 0 {
		t.Fatalf("run during the grace: %+v %v", res, err)
	}
	if n := count(t, chatDB, `SELECT count(*) FROM messages WHERE from_account=$1 OR to_account=$1`, gone); n != 2 {
		t.Fatalf("chat during the grace: %d messages", n)
	}
	if n := count(t, savesDB, `SELECT count(*) FROM saves WHERE account_id=$1`, gone); n != 1 {
		t.Fatalf("saves during the grace: %d", n)
	}
	if _, err := coreHTTP.Login(ctx, goneEmail, "correct horse battery"); !errors.As(err, new(*core.DeletionPending)) {
		t.Fatalf("login during the grace = %v, want DeletionPending", err)
	}

	// The grace period runs out.
	if _, err := coreDB.Exec(ctx, `UPDATE accounts SET erase_after = now() - interval '1 second' WHERE id=$1`, gone); err != nil {
		t.Fatal(err)
	}
	res, err := f.RunOnce(ctx)
	if err != nil || res.Erased < 1 || res.Failed != 0 {
		t.Fatalf("run after the grace: %+v %v", res, err)
	}

	// Every service is empty for the account.
	for q, pool := range map[string]*pgxpool.Pool{
		`SELECT count(*) FROM messages WHERE from_account=$1 OR to_account=$1`:      chatDB,
		`SELECT count(*) FROM saves WHERE account_id=$1`:                            savesDB,
		`SELECT count(*) FROM storage_settings WHERE account_id=$1`:                 savesDB,
		`SELECT count(*) FROM friendships WHERE requester_id=$1 OR addressee_id=$1`: coreDB,
		`SELECT count(*) FROM sessions WHERE account_id=$1`:                         coreDB,
		`SELECT count(*) FROM accounts WHERE id=$1 AND status <> 'deleted'`:         coreDB,
	} {
		if n := count(t, pool, q, gone); n != 0 {
			t.Errorf("%s = %d after erasure", q, n)
		}
	}
	if keys, _ := db.AttachmentKeysForAccount(ctx, gone); len(keys) != 0 {
		t.Errorf("website attachment keys survive: %v", keys)
	}
	if ts, _ := db.ListTickets(ctx, store.TicketFilter{AccountID: gone}, 10); len(ts) != 0 {
		t.Errorf("tickets survive: %d", len(ts))
	}
	if toks, _ := db.PushTokensForAccount(ctx, gone); len(toks) != 0 {
		t.Errorf("push tokens survive: %v", toks)
	}
	if _, err := db.GetMedia(ctx, gone, "avatar"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("media row survives: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "media", "avatars", gone)); !os.IsNotExist(err) {
		t.Errorf("avatar files survive: %v", err)
	}
	if _, err := files.Open(att.Key); !errors.Is(err, attachments.ErrNotFound) {
		t.Errorf("ticket file survives: %v", err)
	}
	if fs, _ := coreGRPC.ListFriends(ctx, friend); len(fs) != 0 {
		t.Errorf("the friend still lists the erased account: %v", fs)
	}
	if due, _ := coreGRPC.DueDeletions(ctx, 10); len(due) != 0 {
		t.Errorf("still due: %v", due)
	}
	// Sign-in fails exactly as for an address nobody has.
	if _, err := coreHTTP.Login(ctx, goneEmail, "correct horse battery"); !errors.Is(err, core.ErrInvalidCredentials) {
		t.Errorf("login after erasure = %v, want ErrInvalidCredentials", err)
	}
	if _, err := coreHTTP.RestoreLogin(ctx, goneEmail, "correct horse battery"); !errors.Is(err, core.ErrInvalidCredentials) {
		t.Errorf("restore after erasure = %v, want ErrInvalidCredentials", err)
	}
	// A second run finds nothing to do.
	if res, err := f.RunOnce(ctx); err != nil || res.Due != 0 {
		t.Errorf("second run: %+v %v", res, err)
	}
}
