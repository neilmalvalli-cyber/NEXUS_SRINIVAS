// Command nexus-api is a small HTTP/JSON adapter in front of NEXUS for the
// web dashboard (frontend/). It is not part of the engine:
//
//   - the main database is opened in-process through the public kvdb
//     package (the same calls the kvdb CLI makes);
//   - crash demonstrations use a separate demo database and a real
//     `kvdb serve` child process that is killed with TerminateProcess;
//   - torture runs start the real `nexus-torture` executable with validated
//     arguments and read the files it writes.
//
// It listens on 127.0.0.1 by default and has no authentication: it is a
// local demonstration tool. The contract is docs/FRONTEND_INTEGRATION.md.
//
//	go build -o . ./src/cmd/kvdb ./src/cmd/nexus-api ./torture2/cmd/nexus-torture
//	.\nexus-api.exe --db data\nexus.kv
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"
)

func main() {
	cfg := Config{}
	flag.StringVar(&cfg.Addr, "addr", "127.0.0.1:8080", "listen address (keep it on localhost: there is no authentication)")
	flag.StringVar(&cfg.DBPath, "db", "nexus-data/nexus.kv", "main database file (created if missing)")
	flag.Int64Var(&cfg.MaxBytes, "max-bytes", 0, "size cap passed to kvdb.Open (0 = unlimited)")
	flag.StringVar(&cfg.WorkDir, "work", "nexus-data/api-work", "directory for the crash-demo database and torture runs")
	flag.StringVar(&cfg.KvdbPath, "kvdb", "", "kvdb executable used by the crash demo and torture runs (default: next to nexus-api, then the current directory)")
	flag.StringVar(&cfg.TorturePath, "torture", "", "nexus-torture executable (default: next to nexus-api, then the current directory)")
	flag.StringVar(&cfg.EvidenceDir, "evidence", "docs/results/final", "directory of recorded validation results served read-only")
	flag.StringVar(&cfg.PerfHistory, "perf-history", "docs/results/optimization/history.json", "recorded optimization history served read-only by GET /api/perf/history")
	flag.StringVar(&cfg.AllowOrigin, "allow-origin", "http://localhost:3000", "CORS origin of the dashboard (comma-separated; empty disables CORS)")
	flag.Parse()

	s, err := New(cfg)
	if err != nil {
		log.Fatalf("nexus-api: %v", err)
	}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	fmt.Fprintf(os.Stderr, "nexus-api: listening on http://%s  db=%s  kvdb=%s  nexus-torture=%s\n",
		cfg.Addr, s.dbAbs, orNone(s.kvdb), orNone(s.torture.exe))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.Close()
		log.Fatalf("nexus-api: %v", err)
	}
	if err := s.Close(); err != nil {
		log.Fatalf("nexus-api: closing: %v", err)
	}
}

func orNone(s string) string {
	if s == "" {
		return "(not found)"
	}
	return s
}
