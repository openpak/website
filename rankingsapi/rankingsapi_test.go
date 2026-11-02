package rankingsapi

import (
	"context"
	"net/http/httptest"
	"testing"
)

type src struct{}

func (src) Boards(context.Context) ([]Board, error) {
	return []Board{{ID: "1", Name: "Course", Kind: KindTime, Order: OrderAsc, Unit: "ms"}}, nil
}

func (src) Rows(_ context.Context, id string, q Query) (Page, error) {
	if id != "1" {
		return Page{}, ErrNoBoard
	}
	var all []Row
	for i := 0; i < 30; i++ {
		all = append(all, Row{PID: string(rune('a'+i%26)) + string(rune('0'+i/26)), Score: int64(60000 + i*1000)})
	}
	return Window(KindTime, all, q), nil
}

func TestRoundTrip(t *testing.T) {
	ts := httptest.NewServer(Handler("wiiu", src{}))
	defer ts.Close()
	c := NewClient(ts.URL)
	ctx := context.Background()
	l, err := c.Boards(ctx)
	if err != nil || l.Network != "wiiu" || len(l.Boards) != 1 || l.Boards[0].Kind != KindTime {
		t.Fatalf("boards: %+v %v", l, err)
	}
	p, err := c.Rows(ctx, "1", Query{Limit: 5, Offset: 2, Around: "k0"})
	if err != nil || p.Total != 30 || len(p.Rows) != 5 || p.Rows[0].Rank != 3 || p.Rows[0].Display != "1:02.000" {
		t.Fatalf("rows: %+v %v", p, err)
	}
	if len(p.Around) != 11 || p.Around[5].PID != "k0" {
		t.Fatalf("around: %+v", p.Around)
	}
	if _, err := c.Rows(ctx, "nope", Query{}); err != ErrNoBoard {
		t.Fatalf("unknown board: %v", err)
	}
}

func TestLimitClamp(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("x", src{}).ServeHTTP(rec, httptest.NewRequest("GET", "/internal/rankings/boards/1?limit=9999", nil))
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	rec = httptest.NewRecorder()
	Handler("x", src{}).ServeHTTP(rec, httptest.NewRequest("GET", "/internal/rankings/boards/1?limit=x", nil))
	if rec.Code != 400 {
		t.Fatal(rec.Code)
	}
}

func TestFormat(t *testing.T) {
	if Number(1234567) != "1,234,567" || Number(-999) != "-999" || Number(1000) != "1,000" || NeutralName(3) != "Board 3" {
		t.Fatal("format")
	}
}
