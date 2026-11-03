package main

import (
	"compress/gzip"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

var dayFile = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.log(\.gz)?$`)

// prune gzips day files older than GzipAfter days and deletes files
// older than KeepDays.
func prune(ctx context.Context, cfg config, now time.Time) error {
	today := now.UTC().Truncate(24 * time.Hour)
	gzipBefore := today.AddDate(0, 0, -cfg.GzipAfter)
	deleteBefore := today.AddDate(0, 0, -cfg.KeepDays)
	var zipped, removed int
	dirs, _ := filepath.Glob(filepath.Join(cfg.Dir, "*"))
	for _, d := range dirs {
		fis, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, fi := range fis {
			m := dayFile.FindStringSubmatch(fi.Name())
			if m == nil {
				continue
			}
			day, err := time.Parse("2006-01-02", m[1])
			if err != nil {
				continue
			}
			p := filepath.Join(d, fi.Name())
			switch {
			case day.Before(deleteBefore):
				if os.Remove(p) == nil {
					removed++
				}
			case m[2] == "" && day.Before(gzipBefore):
				if err := gzipFile(p); err != nil {
					log.Printf("gzip %s: %v", p, err)
				} else {
					zipped++
				}
			}
		}
	}
	var st syscall.Statfs_t
	if syscall.Statfs(cfg.Dir, &st) == nil {
		log.Printf("files: %d gzipped, %d deleted; %d GiB free", zipped, removed, st.Bavail*uint64(st.Bsize)>>30)
	}
	return nil
}

func gzipFile(p string) error {
	in, err := os.Open(p)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(p+".gz.new", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	zw, _ := gzip.NewWriterLevel(out, gzip.BestCompression)
	_, err = io.Copy(zw, in)
	if e := zw.Close(); err == nil {
		err = e
	}
	if e := out.Close(); err == nil {
		err = e
	}
	if err != nil {
		os.Remove(p + ".gz.new")
		return err
	}
	if err := os.Rename(p+".gz.new", p+".gz"); err != nil {
		return err
	}
	return os.Remove(p)
}
