# Local vendor patches

`vendor/github.com/muesli/termenv/termenv_unix.go`: OSC color-query timeout is
150 ms instead of upstream's 5 seconds. Bubble Tea v1 queries the terminal
background in its package initializer, before application code can configure
it. Terminals that do not answer OSC queries otherwise delay every launch by
five seconds. The existing COLORFGBG/default-color fallback remains in place;
responsive terminals still report their actual color.

Reapply this patch after `go mod vendor` until the dependency provides a bounded
startup query or stops making it during initialization. The subprocess PTY
regression test `TestStartupSilentTerminal` checks startup without answering
terminal queries.
