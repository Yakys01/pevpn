package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/xeri/upvpn"
	"github.com/xeri/upvpn/config"
)

func run(args []string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(os.Stdout)
		return 0
	case "serve":
		return runServe(args[1:])
	}

	fs := flag.NewFlagSet("upvpn", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { usage(os.Stderr) }
	show := fs.Bool("show", false, "")
	asJSON := fs.Bool("json", false, "")
	check := fs.Bool("check", false, "")
	selfTest := fs.Bool("self-test", false, "")
	printConfig := fs.Bool("print-config", false, "")
	providerName := fs.String("provider", "", "")
	configPath := fs.String("config", "", "")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := resolveConfig(*configPath, *providerName, "", 0)
	if err != nil {
		fail(err)
		return 1
	}
	switch {
	case *printConfig:
		data, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Println(string(data))
		return 0
	case *show || *asJSON:
		return runShow(cfg, *asJSON)
	case *check:
		return runProbe(cfg, false)
	case *selfTest:
		return runProbe(cfg, true)
	}
	if rest := fs.Args(); len(rest) > 0 {
		return runWrap(cfg, rest)
	}
	usage(os.Stderr)
	return 2
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("upvpn serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	port := fs.Int("port", 0, "")
	bind := fs.String("bind", "", "")
	providerName := fs.String("provider", "", "")
	configPath := fs.String("config", "", "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := resolveConfig(*configPath, *providerName, *bind, *port)
	if err != nil {
		fail(err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client, err := upvpn.New(upvpn.Options{Config: cfg, Logger: logger()})
	if err != nil {
		fail(err)
		return 1
	}
	if err := client.Serve(ctx, net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port))); err != nil {
		fail(err)
		if errors.Is(err, upvpn.ErrNotConnected) {
			return 2
		}
		return 1
	}
	return 0
}

func runShow(cfg config.Config, asJSON bool) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client, err := upvpn.New(upvpn.Options{Config: cfg, Logger: logger()})
	if err != nil {
		fail(err)
		return 1
	}
	snap, err := client.Snapshot(ctx)
	if err != nil {
		fail(err)
		return 1
	}
	if asJSON {
		out := stateJSON{Connected: snap.Connected, Location: snap.Location}
		for _, t := range snap.Targets {
			out.Targets = append(out.Targets, targetJSON{
				Provider: t.Provider, Scheme: t.Scheme, Endpoint: t.Addr(),
				Username: mask(t.Username),
			})
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(data))
		return 0
	}
	if len(snap.Targets) == 0 {
		fail(upvpn.ErrNotConnected)
		return 2
	}
	for _, t := range snap.Targets {
		fmt.Printf("provider=%s scheme=%s endpoint=%s location=%s connected=%t user=%s\n",
			t.Provider, t.Scheme, t.Addr(), snap.Location, snap.Connected, mask(t.Username))
	}
	return 0
}

func runProbe(cfg config.Config, self bool) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var checked, failed int
	for _, p := range cfg.Providers {
		sub := cfg
		sub.Providers = []config.Provider{p}
		client, err := upvpn.New(upvpn.Options{Config: sub, Logger: logger()})
		if err != nil {
			fail(err)
			return 1
		}
		var (
			ip  string
			ok  bool
			perr error
		)
		if self {
			ip, ok, perr = client.SelfTest(ctx)
		} else {
			ip, ok, perr = client.Check(ctx)
		}
		switch {
		case errors.Is(perr, upvpn.ErrNotConnected):
			fmt.Printf("SKIP: %-14s no disponible\n", p.Name)
		case perr != nil:
			fmt.Printf("FAIL: %-14s %v\n", p.Name, perr)
			failed++
		case ok:
			fmt.Printf("PASS: %-14s salida=%s\n", p.Name, ip)
			checked++
		default:
			fmt.Printf("FAIL: %-14s salida=%s (no es la IP del proxy)\n", p.Name, ip)
			failed++
		}
	}
	if failed > 0 {
		return 1
	}
	if checked == 0 {
		fail(upvpn.ErrNotConnected)
		return 2
	}
	return 0
}

func runWrap(cfg config.Config, cmd []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client, err := upvpn.New(upvpn.Options{Config: cfg, Logger: logger()})
	if err != nil {
		fail(err)
		return 1
	}
	snap, err := client.Snapshot(ctx)
	if err != nil {
		fail(err)
		return 1
	}
	if len(snap.Targets) == 0 {
		fail(upvpn.ErrNotConnected)
		return 2
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fail(err)
		return 1
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go client.ServeListener(cctx, ln)

	proxyURL := "http://" + ln.Addr().String()
	child := exec.Command(cmd[0], cmd[1:]...)
	child.Env = append(os.Environ(),
		"http_proxy="+proxyURL, "https_proxy="+proxyURL, "all_proxy="+proxyURL,
		"HTTP_PROXY="+proxyURL, "HTTPS_PROXY="+proxyURL, "ALL_PROXY="+proxyURL)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		fail(err)
		return 127
	}
	if err := child.Wait(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fail(err)
		return 1
	}
	return 0
}

func resolveConfig(configPath, providerName, bind string, port int) (config.Config, error) {
	path := firstNonEmpty(configPath, os.Getenv("UPVPN_CONFIG"), config.Path())
	cfg, err := config.Load(path)
	if err != nil {
		return cfg, err
	}
	if name := firstNonEmpty(providerName, os.Getenv("UPVPN_PROVIDER")); name != "" {
		cfg.Providers = []config.Provider{{
			Name:        name,
			ProfileDir:  os.Getenv("UPVPN_PROFILE_DIR"),
			ExtensionID: os.Getenv("UPVPN_EXTENSION_ID"),
		}}
	}
	if v := os.Getenv("UPVPN_BIND"); v != "" {
		cfg.Bind = v
	}
	if v := os.Getenv("UPVPN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Port = n
		}
	}
	if bind != "" {
		cfg.Bind = bind
	}
	if port != 0 {
		cfg.Port = port
	}
	return cfg, nil
}

type targetJSON struct {
	Provider string `json:"provider"`
	Scheme   string `json:"scheme"`
	Endpoint string `json:"endpoint"`
	Username string `json:"username"`
}

type stateJSON struct {
	Connected bool         `json:"connected"`
	Location  string       `json:"location"`
	Targets   []targetJSON `json:"targets"`
}

func mask(token string) string {
	if len(token) <= 12 {
		return token
	}
	return token[:8] + "..." + token[len(token)-4:]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "upvpn:", err)
}
