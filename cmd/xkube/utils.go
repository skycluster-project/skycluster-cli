package xkube

import (
	"fmt"
	"os"
	"time"
)

// runWithSpinner runs f() while showing a simple spinner and message on stderr.
// It returns f()'s error. The spinner writes to stderr to avoid clobbering stdout.
func runWithSpinner(msg string, f func() error) error {
	stop := make(chan struct{})
	spinnerDone := make(chan struct{})
	resultCh := make(chan error, 1)

	// spinner goroutine
	go func() {
		defer close(spinnerDone)
		chars := []rune{'|', '/', '-', '\\'}
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				fmt.Fprintf(os.Stderr, "\r%s... %c", msg, chars[i%len(chars)])
				i++
				time.Sleep(150 * time.Millisecond)
			}
		}
	}()

	// run the work
	go func() {
		resultCh <- f()
	}()

	// wait for work to finish
	err := <-resultCh

	// signal spinner to stop and wait for it to actually exit (important!)
	close(stop)
	<-spinnerDone

	// clear the spinner line (carriage return + ANSI clear line) and print final status on its own line
	fmt.Fprint(os.Stderr, "\r\033[K")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s... failed\n", msg)
		fmt.Fprintf(os.Stderr, "error: %v\n", err) // will be on the next line
		return err
	}

	fmt.Fprintf(os.Stderr, "%s... done\n", msg)
	return nil
}