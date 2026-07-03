package main

import (
	"fmt"
	"os"
	"time"

	"github.com/dogmatiq/ferrite"
	"github.com/getsentry/sentry-go"
	"github.com/spf13/cobra"
)

const sentryFlushTimeout = 2 * time.Second

var (
	sentryDSN = ferrite.String(
		"SENTRY_DSN",
		"Sentry DSN used to report CLI errors and panics. Leave empty to disable Sentry.",
	).
		WithDefault("").
		WithSensitiveContent().
		Required()

	rootCmd = &cobra.Command{
		Use:   "kakak",
		Short: "kakak tolong!",
	}
)

func main() {
	ferrite.Init()

	sentryEnabled, err := initSentry()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if sentryEnabled {
		defer sentry.Flush(sentryFlushTimeout)
		defer capturePanic()
	}

	if err := rootCmd.Execute(); err != nil {
		if sentryEnabled {
			sentry.CaptureException(err)
			sentry.Flush(sentryFlushTimeout)
		}

		fmt.Println(err)
		os.Exit(1)
	}
}

func initSentry() (bool, error) {
	dsn := sentryDSN.Value()
	if dsn == "" {
		return false, nil
	}

	if err := sentry.Init(sentry.ClientOptions{Dsn: dsn}); err != nil {
		return false, fmt.Errorf("failed to initialize sentry: %w", err)
	}

	return true, nil
}

func capturePanic() {
	if v := recover(); v != nil {
		sentry.CurrentHub().Recover(v)
		panic(v)
	}
}
