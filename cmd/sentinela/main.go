// Command sentinela is the single control-plane binary (docs/ARCHITECTURE.md).
//
//	sentinela serve        API, auth, SPA, stream proxy, camera control, events
//	sentinela record       ffmpeg recorders, segment indexer, retention (ADR-0007)
//	sentinela apps         self-hosted services agent, Docker socket (ADR-0021)
//	sentinela user ...     add | passwd | del | list  (password read from stdin)
//	sentinela healthcheck  exit 0 if the local API answers /healthz
//	sentinela check-config validate the config + secrets, list the cameras
//	sentinela go2rtc-config print go2rtc's camera streams (no secrets needed)
//	sentinela version
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // night-mode schedules work even without host zoneinfo

	"golang.org/x/crypto/acme/autocert"

	"github.com/LUKER2DW/sentinela/internal/api"
	"github.com/LUKER2DW/sentinela/internal/apps"
	"github.com/LUKER2DW/sentinela/internal/auth"
	"github.com/LUKER2DW/sentinela/internal/camera/onvif"
	"github.com/LUKER2DW/sentinela/internal/config"
	"github.com/LUKER2DW/sentinela/internal/publiccert"
	"github.com/LUKER2DW/sentinela/internal/recording"
	"github.com/LUKER2DW/sentinela/internal/store"
	"github.com/LUKER2DW/sentinela/web"
)

var version = "dev"

func main() {
	api.Version = version
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fl := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := fl.String("config", envOr("SENTINELA_CONFIG", "/etc/sentinela/config.yaml"), "config file")
	role := fl.String("role", "viewer", "role for `user add` (admin|operator|viewer)")
	_ = fl.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "serve":
		err = serve(ctx, log, *cfgPath)
	case "record":
		err = record(ctx, log, *cfgPath)
	case "apps":
		err = appsAgent(ctx, log, *cfgPath)
	case "user":
		err = userCmd(ctx, *cfgPath, *role, fl.Args())
	case "healthcheck":
		err = healthcheck(*cfgPath)
	case "check-config":
		err = checkConfig(*cfgPath)
	case "go2rtc-config":
		var cfg *config.Config
		if cfg, err = config.LoadLayout(*cfgPath); err == nil {
			err = cfg.WriteGo2RTC(os.Stdout)
		}
	case "version":
		fmt.Println(version)
	default:
		err = fmt.Errorf("unknown command %q (serve|record|apps|user|healthcheck|check-config|go2rtc-config|version)", cmd)
	}
	if err != nil {
		log.Error("fatal", "cmd", cmd, "err", err)
		os.Exit(1)
	}
}

// checkConfig loads the config with its secrets and prints the cameras
// (scripts/add-camera.sh runs it on a candidate config before installing it).
func checkConfig(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	for _, c := range cfg.Cameras {
		fmt.Printf("%s\t%s\t%d\t%d\t%s\n", c.ID, c.Host, c.RTSPPort, c.DVRIPPort, c.Name)
	}
	return nil
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func serve(ctx context.Context, log *slog.Logger, cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	var webFS fs.FS
	if sub, err := fs.Sub(web.Dist, "dist"); err == nil {
		if _, err := fs.Stat(sub, "index.html"); err == nil {
			webFS = sub
		}
	}
	if webFS == nil {
		log.Warn("web UI not embedded; build it with `npm run build` in web/")
	}

	srv, err := api.New(cfg, st, webFS, log)
	if err != nil {
		return err
	}
	defer srv.Close()

	var wg sync.WaitGroup
	for i := range cfg.Cameras {
		cam := &cfg.Cameras[i]
		w := &onvif.Watcher{CameraID: cam.ID, Client: onvif.NewClient(cam.Host, cam.ONVIFPort, cam.User, cam.Password),
			Store: st, Log: log, Sink: srv.HandleEvent, Quiet: srv.MotionQuiet, Health: srv.NoteEventWatch}
		wg.Add(1)
		go func() { defer wg.Done(); w.Run(ctx) }()
	}
	wg.Add(2)
	go func() { defer wg.Done(); maintenance(ctx, log, cfg, st, srv) }()
	go func() { defer wg.Done(); srv.WatchDetector(ctx) }()

	servers := []*http.Server{{Addr: cfg.Listen, Handler: srv.Handler()}}
	// ADR-0019 trial: public listeners, mandatory 2FA and the closed door.
	if l := cfg.Remote.FunnelListen; l != "" {
		servers = append(servers, &http.Server{Addr: l, Handler: srv.PublicHandler(api.ZoneFunnel)})
	}
	if p := cfg.Remote.Public; p.Domain != "" {
		var tc *tls.Config
		if ip := net.ParseIP(p.Domain); ip != nil && p.SelfSigned {
			// The ISP blocks 80 and 443: a self-signed certificate the app pins.
			c, fp, err := publiccert.SelfSigned(filepath.Join(cfg.DataDir, "acme", "self"), ip.String())
			if err != nil {
				return fmt.Errorf("public self-signed certificate: %w", err)
			}
			log.Info("public listener uses a self-signed certificate", "ip", ip.String(), "sha256", fp)
			srv.SetPublicCertPin(fp)
			tc = c
		} else if ip != nil {
			// No domain: a short-lived Let's Encrypt certificate for the fixed IP.
			m := &publiccert.Manager{IP: ip.String(), Email: p.Email, Dir: filepath.Join(cfg.DataDir, "acme", "ip"), Log: log, HTTP01: p.HTTP01Listen != ""}
			if p.HTTP01Listen != "" {
				// Plain HTTP that only answers Let's Encrypt (router WAN 80 -> here).
				servers = append(servers, &http.Server{Addr: p.HTTP01Listen, Handler: m.HTTPHandler()})
			}
			wg.Add(1)
			go func() { defer wg.Done(); m.Run(ctx) }()
			tc = m.TLSConfig()
		} else {
			tc = publicTLS(cfg.DataDir, p.Domain, p.Email)
		}
		servers = append(servers, &http.Server{Addr: p.Listen, Handler: srv.PublicHandler(api.ZonePublic), TLSConfig: tc})
	}
	errc := make(chan error, len(servers))
	for _, hs := range servers {
		hs.ReadHeaderTimeout, hs.IdleTimeout, hs.MaxHeaderBytes = 10*time.Second, 2*time.Minute, 64<<10
		// HTTP/2 (TLS listeners) lets a client send at most 1 MiB per round
		// trip by default, which caps Nuvem uploads on far links (~100 ms
		// RTT → ~10 MB/s). Raise it to the documented maximum (< 4 MiB);
		// buffers fill only while the disk lags behind the network.
		const h2Window = 4<<20 - 1
		hs.HTTP2 = &http.HTTP2Config{MaxReceiveBufferPerStream: h2Window, MaxReceiveBufferPerConnection: h2Window}
		go func() {
			log.Info("sentinela serving", "addr", hs.Addr, "tls", hs.TLSConfig != nil, "version", version, "cameras", len(cfg.Cameras))
			if hs.TLSConfig != nil {
				errc <- hs.ListenAndServeTLS("", "")
			} else {
				errc <- hs.ListenAndServe()
			}
		}()
	}
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, hs := range servers {
		_ = hs.Shutdown(shCtx)
	}
	wg.Wait()
	return nil
}

// publicTLS gets and renews a Let's Encrypt certificate for domain only
// (TLS-ALPN-01 on the same port, so the router forwards just 443 → listen).
func publicTLS(dataDir, domain, email string) *tls.Config {
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domain),
		Cache:      autocert.DirCache(filepath.Join(dataDir, "acme")),
		Email:      email,
	}
	c := m.TLSConfig()
	c.MinVersion = tls.VersionTLS12
	return c
}

// snapshotMaxAge keeps snapshots back to the oldest event plus a day; with
// no events left, 30 days.
func snapshotMaxAge(ctx context.Context, st *store.Store) time.Duration {
	if oldest, ok, err := st.OldestEventStart(ctx); err == nil && ok {
		return time.Since(oldest) + 24*time.Hour
	}
	return 30 * 24 * time.Hour
}

// maintenance purges sessions hourly and writes a nightly DB backup.
func maintenance(ctx context.Context, log *slog.Logger, cfg *config.Config, st *store.Store, srv *api.Server) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	lastBackup := ""
	for {
		_ = st.PurgeExpiredSessions(ctx)
		// Event snapshots live as long as their events, which retention
		// keeps as long as the recordings (ADR-0015).
		srv.PurgeSnapshots(snapshotMaxAge(ctx, st))
		srv.PruneCaches()
		_ = st.PurgeSeenBefore(ctx, time.Now().Add(-31*24*time.Hour))
		day := time.Now().UTC().Format("20060102")
		if day != lastBackup {
			dir := filepath.Join(cfg.DataDir, "backup")
			if err := os.MkdirAll(dir, 0o750); err == nil {
				dst := filepath.Join(dir, "sentinela-"+day+".db")
				if err := st.Backup(ctx, dst); err != nil {
					log.Warn("db backup failed", "err", err)
				} else {
					lastBackup = day
					pruneBackups(dir, 7)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func pruneBackups(dir string, keep int) {
	m, _ := filepath.Glob(filepath.Join(dir, "sentinela-*.db"))
	sort.Strings(m)
	for len(m) > keep {
		_ = os.Remove(m[0])
		m = m[1:]
	}
}

func record(ctx context.Context, log *slog.Logger, cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	root := filepath.Join(cfg.DataDir, "recordings")
	marker := cfg.Recording.VolumeMarkerPath(cfg.DataDir)
	if _, err := os.Stat(marker); marker != "" && err != nil {
		// Recorders wait for the disk; nothing is created on the system disk.
		log.Error("recording disk not mounted: volume marker missing", "marker", marker)
	} else if err := os.MkdirAll(root, 0o750); err != nil {
		return err
	}
	// Recording must not depend on the database (ADR-0007): if it cannot be
	// opened, record anyway and retry; the indexer catches up later.
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		log.Error("database unavailable; recording without index", "err", err)
		st = nil
	}
	var wg sync.WaitGroup
	var recorders []*recording.Recorder
	for i := range cfg.Cameras {
		cam := &cfg.Cameras[i]
		if !cam.Recording() {
			log.Info("recording disabled", "camera", cam.ID)
			continue
		}
		r := recording.NewRecorder(cam, cfg.Recording, root, st, log)
		r.Marker = marker
		recorders = append(recorders, r)
		wg.Add(1)
		go func() { defer wg.Done(); r.Run(ctx) }()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for st == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
			}
			if s, err := store.Open(ctx, cfg.DBPath); err == nil {
				st = s
				for _, r := range recorders {
					r.SetStore(s)
				}
				log.Info("database available again; indexing resumes")
			}
		}
		defer st.Close()
		ix := recording.NewIndexer(root, st, log)
		ret := recording.NewRetention(root, st, cfg, log)
		var inner sync.WaitGroup
		inner.Add(2)
		go func() { defer inner.Done(); ix.Run(ctx, 15*time.Second) }()
		go func() { defer inner.Done(); ret.Run(ctx, 5*time.Minute) }()
		inner.Wait()
	}()
	log.Info("sentinela recording", "cameras", len(cfg.Cameras), "root", root)
	wg.Wait()
	return nil
}

// appsAgent runs the self-hosted services agent (ADR-0021): the only
// Sentinela process with the Docker socket. It answers the API on a
// loopback address with the token the API writes to <data>/apps-agent.
func appsAgent(ctx context.Context, log *slog.Logger, cfgPath string) error {
	cfg, err := config.LoadApps(cfgPath)
	if err != nil {
		return err
	}
	if !cfg.Apps.Enabled {
		log.Info("apps agent idle: apps.enabled is false")
		<-ctx.Done()
		return nil
	}
	tokenPath := filepath.Join(cfg.DataDir, "apps-agent", "token")
	var token string
	for token == "" {
		if b, err := os.ReadFile(tokenPath); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
			token = strings.TrimSpace(string(b))
			break
		}
		log.Info("apps agent waiting for the API token", "path", tokenPath)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
	sources := map[string]apps.Source{}
	agent := apps.NewAgent(cfg.Apps, apps.NewDocker(cfg.Apps.Docker), sources, log)
	sources[apps.SourceGitHub] = apps.GitHubSource{GH: apps.NewGitHub(agent.GitHubToken)}
	if cfg.Apps.LocalRoot != "" {
		sources[apps.SourceLocal] = apps.LocalSource{Root: cfg.Apps.LocalRoot}
	}
	if err := agent.Load(); err != nil {
		return err
	}
	hs := &http.Server{Addr: cfg.Apps.Agent, Handler: agent.Handler(token), ReadHeaderTimeout: 10 * time.Second}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); agent.Run(ctx) }()
	errc := make(chan error, 1)
	go func() {
		log.Info("apps agent serving", "addr", hs.Addr, "state", cfg.Apps.StateDir)
		errc <- hs.ListenAndServe()
	}()
	select {
	case err = <-errc:
	case <-ctx.Done():
	}
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = hs.Shutdown(shCtx)
	wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func userCmd(ctx context.Context, cfgPath, role string, args []string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	// Go's flag package stops at the first non-flag, so `user add -role
	// admin bob` would leave "-role" in args: accept -role anywhere.
	args, role, err = splitRoleFlag(args, role)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: sentinela user add|passwd|del|list [username] [-role r]")
	}
	if len(args) > 1 && strings.HasPrefix(args[1], "-") && args[0] != "del" {
		return fmt.Errorf("usernames cannot start with '-': %q", args[1])
	}
	readPW := func() (string, error) {
		fmt.Fprint(os.Stderr, "New password (min 10 chars), then Enter: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	switch args[0] {
	case "list":
		users, err := st.ListUsers(ctx)
		if err != nil {
			return err
		}
		for _, u := range users {
			fmt.Printf("%-20s %s\n", u.Username, u.Role)
		}
		return nil
	case "del":
		if len(args) < 2 {
			return errors.New("username required")
		}
		u, err := st.UserByName(ctx, args[1])
		if err != nil {
			return err
		}
		if u.Role == auth.RoleAdmin {
			if n, err := st.CountAdmins(ctx); err != nil || n <= 1 {
				return errors.New("cannot delete the last admin")
			}
		}
		if err := st.DeleteUser(ctx, u.ID); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "ok")
		return nil
	case "add", "passwd":
		if len(args) < 2 {
			return errors.New("username required")
		}
		pw, err := readPW()
		if err != nil {
			return err
		}
		hash, err := auth.HashPassword(pw)
		if err != nil {
			return err
		}
		if args[0] == "add" {
			if !auth.ValidRole(role) {
				return errors.New("invalid role")
			}
			_, err = st.CreateUser(ctx, args[1], hash, role)
		} else {
			var u *store.User
			if u, err = st.UserByName(ctx, args[1]); err == nil {
				err = st.UpdateUserPassword(ctx, u.ID, hash)
			}
		}
		if err == nil {
			fmt.Fprintln(os.Stderr, "ok")
		}
		return err
	}
	return fmt.Errorf("unknown user subcommand %q", args[0])
}

// splitRoleFlag removes "-role x", "--role x", "-role=x" from args.
func splitRoleFlag(args []string, role string) ([]string, string, error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--": // the rest is literal (e.g. `user del -- -odd`)
			return append(out, args[i+1:]...), role, nil
		case a == "-role" || a == "--role":
			if i+1 == len(args) {
				return nil, "", errors.New("-role needs a value")
			}
			role = args[i+1]
			i++
		case strings.HasPrefix(a, "-role=") || strings.HasPrefix(a, "--role="):
			role = a[strings.Index(a, "=")+1:]
		default:
			out = append(out, a)
		}
	}
	return out, role, nil
}

func healthcheck(cfgPath string) error {
	listen := ":8080"
	if cfg, err := config.Load(cfgPath); err == nil {
		listen = cfg.Listen
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return err
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: %s", resp.Status)
	}
	return nil
}
