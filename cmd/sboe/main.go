// sboe: scraping de subastas + servidor web (admin y platform).
//
//	sboe [run] [-source=boe,...]   ejecuta los jobs una vez y termina
//	sboe serve                     servidor web + jobs programados + alertas
//	sboe createuser -email x -password y [-admin]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sboe/internal/alerts"
	"sboe/internal/auth"
	"sboe/internal/config"
	"sboe/internal/notify"
	"sboe/internal/runner"
	"sboe/internal/scheduler"
	"sboe/internal/store"
	"sboe/internal/web"
)

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "run":
		err = runCmd(args)
	case "serve":
		err = serveCmd(args)
	case "createuser":
		err = createUserCmd(args)
	default:
		err = fmt.Errorf("subcomando desconocido %q (usa run, serve o createuser)", cmd)
	}
	if err != nil {
		slog.Error("terminado con errores", "err", err)
		os.Exit(1)
	}
}

func setupLog(verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

func openStore(cfg config.Config) (*store.Store, error) {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("abriendo base de datos: %w", err)
	}
	return st, nil
}

func runCmd(args []string) error {
	fl := flag.NewFlagSet("run", flag.ExitOnError)
	sourceFlag := fl.String("source", "", "jobs a ejecutar separados por coma (por defecto todos)")
	dumpDir := fl.String("dump-html", "", "directorio donde guardar el HTML descargado (depuración)")
	verbose := fl.Bool("v", false, "logs de depuración")
	noAlerts := fl.Bool("no-alerts", false, "no enviar alertas 'cuando corran los jobs'")
	full := fl.Bool("full", false, "descargar el detalle de todas las subastas, aunque no hayan cambiado")
	fl.Parse(args)
	setupLog(*verbose)

	cfg := config.Load()
	reg := runner.Registry(cfg, *dumpDir)
	names := runner.Names(reg)
	if *sourceFlag != "" {
		names = nil
		for _, n := range strings.Split(*sourceFlag, ",") {
			n = strings.TrimSpace(n)
			if _, ok := reg[n]; !ok {
				return fmt.Errorf("job desconocido %q (disponibles: %s)", n, strings.Join(runner.Names(reg), ", "))
			}
			names = append(names, n)
		}
	}

	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var hook runner.FinishedFunc
	if !*noAlerts {
		hook = alerts.New(st, notify.New(cfg.Mail), cfg.BaseURL, cfg.Location).OnRun
	}
	mgr := runner.NewManager(ctx, st, reg, hook)
	slog.Info("ejecutando jobs", "jobs", strings.Join(names, ","), "db", cfg.DBPath)
	return mgr.RunAll(ctx, names, runner.Options{Trigger: runner.TriggerCLI, Full: *full})
}

func serveCmd(args []string) error {
	fl := flag.NewFlagSet("serve", flag.ExitOnError)
	verbose := fl.Bool("v", false, "logs de depuración")
	fl.Parse(args)
	setupLog(*verbose)

	cfg := config.Load()
	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	reg := runner.Registry(cfg, "")
	if err := st.EnsureJobs(ctx, runner.Names(reg)); err != nil {
		return err
	}
	if err := st.MarkInterruptedRuns(ctx); err != nil {
		return err
	}
	if n, err := st.CountUsers(ctx); err == nil && n == 0 {
		slog.Warn("no hay usuarios: crea el primer admin con `sboe createuser -email ... -password ... -admin`")
	}

	alertSvc := alerts.New(st, notify.New(cfg.Mail), cfg.BaseURL, cfg.Location)
	mgr := runner.NewManager(ctx, st, reg, alertSvc.OnRun)
	sched := scheduler.New(ctx, st, mgr, cfg.Location, func(ctx context.Context, now time.Time) {
		alertSvc.Daily(ctx, now)
		if now.Minute() == 0 {
			st.DeleteExpiredSessions(ctx)
		}
	})
	if err := sched.Start(); err != nil {
		return err
	}
	defer sched.Stop()

	h, err := web.New(web.Deps{
		Store: st, Auth: auth.New(st, cfg.CookieSecure), Manager: mgr, Scheduler: sched,
		Alerts: alertSvc, Location: cfg.Location,
	})
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr: cfg.Addr, Handler: h,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("servidor escuchando", "addr", cfg.Addr, "base_url", cfg.BaseURL, "db", cfg.DBPath)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("apagando…")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	sched.Stop()
	// ctx ya está cancelado: los jobs en curso se interrumpen y registran su fin.
	mgr.Wait()
	return nil
}

func createUserCmd(args []string) error {
	fl := flag.NewFlagSet("createuser", flag.ExitOnError)
	email := fl.String("email", "", "email del usuario")
	password := fl.String("password", "", "contraseña (mín. 8 caracteres)")
	admin := fl.Bool("admin", false, "crear como administrador")
	fl.Parse(args)
	setupLog(false)
	if *email == "" || *password == "" {
		return errors.New("uso: sboe createuser -email x@y.com -password secreto [-admin]")
	}
	hash, err := auth.HashPassword(*password)
	if err != nil {
		return err
	}
	cfg := config.Load()
	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	role := store.RoleUser
	if *admin {
		role = store.RoleAdmin
	}
	u, err := st.CreateUser(context.Background(), *email, hash, role)
	if errors.Is(err, store.ErrDuplicate) {
		return fmt.Errorf("ya existe un usuario con email %s", *email)
	}
	if err != nil {
		return err
	}
	fmt.Printf("Usuario creado: %s (%s)\n", u.Email, u.Role)
	return nil
}
