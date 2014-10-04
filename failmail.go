package main

import (
	"fmt"
	"github.com/hut8labs/failmail/configure"
	"log"
	"os"
	"time"
)

const VERSION = "0.2.0"

const LOGO = `
     *===================*
    / .-----------------. \
   /  |                 |  \
  +\~~|       :(        |~~/+
  | \_._________________._/ |
  |  /                   \  |
  | /   failmail v%5s   \ |
  |/_______________________\|
`

func main() {
	config := Defaults()

	wroteConfig, err := configure.Parse(config, fmt.Sprintf(LOGO, VERSION))
	if err != nil {
		log.Fatalf("Failed to read configuration: %s", err)
	} else if wroteConfig {
		return
	}

	if config.Version {
		fmt.Fprintf(os.Stderr, "failmail %s\n", VERSION)
		return
	}
	log.Printf("failmail %s, starting up", VERSION)

	// A channel for incoming messages. The listener sends on the channel, and
	// receives are added to a MessageBuffer in the channel consumer below.
	received := make(chan *ReceivedMessage, 64)

	// A channel for outgoing messages.
	sending := make(chan OutgoingMessage, 64)

	// Handle signals for reloading/shutdown.
	done := make(chan TerminationRequest, 1)
	go HandleSignals(done)

	auth, err := config.Auth()
	if err != nil {
		log.Fatalf("failed to parse auth credentials: %s", err)
	}

	tlsConfig, err := config.TLSConfig()

	// The listener talks SMTP to clients, and puts any messages they send onto
	// the `received` channel.
	socket, err := config.Socket()
	if err != nil {
		log.Fatalf("failed to create socket for listener: %s", err)
	}

	reloader := NewReloader()

	listener := &Listener{Logger: logger("listener"), Socket: socket, Auth: auth, TLSConfig: tlsConfig}
	go listener.Listen(received, reloader, config.ShutdownTimeout)

	if config.Pidfile != "" {
		writePidfile(config.Pidfile)
		defer os.Remove(config.Pidfile)
	}

	// A `MessageBuffer` collects incoming messages and decides how to batch
	// them up and when to relay them to an upstream SMTP server.
	buffer := NewMessageBuffer(config.WaitPeriod, config.MaxWait, config.Batch(), config.Group(), config.From)

	// A `RateCounter` watches the rate at which incoming messages are arriving
	// and can determine whether we've exceeded some threshold, for alerting.
	rateCounter := NewRateCounter(config.RateLimit, config.RateWindow)

	// An upstream SMTP server is used to send the summarized messages flushed
	// from the MessageBuffer.
	upstream, err := config.Upstream()
	if err != nil {
		log.Fatalf("failed to create upstream: %s", err)
	}

	// Any messages we were unable to send upstream will be written to this
	// maildir.
	failedMaildir := &Maildir{Path: config.FailDir}
	if err := failedMaildir.Create(); err != nil {
		log.Fatalf("failed to create maildir for failed messages at %s: %s", config.FailDir, err)
	}

	if config.Script != "" {
		runner, err := runScript(config.Script)
		if err != nil {
			log.Fatalf("failed to run script file %s: %s", config.Script, err)
		}
		go runner(done)
	}
	go ListenHTTP(config.BindHTTP, buffer)

	renderer := config.SummaryRenderer()
	go run(renderer, buffer, rateCounter, config.RateCheck, reloader, received, sending, done, config.RelayAll)

	sendUpstream(sending, upstream, failedMaildir)

	if err := reloader.ReloadIfNecessary(); err != nil {
		log.Fatalf("failed to reload: %s", err)
	}
}

func sendUpstream(sending <-chan OutgoingMessage, upstream Upstream, failedMaildir *Maildir) {
	for msg := range sending {
		if sendErr := upstream.Send(msg); sendErr != nil {
			log.Printf("couldn't send message: %s", sendErr)
			if saveErr := failedMaildir.Write([]byte(msg.Contents())); saveErr != nil {
				log.Printf("couldn't save message: %s", saveErr)
			}
		}
	}
	log.Printf("done sending")
}

func run(renderer SummaryRenderer, buffer *MessageBuffer, rateCounter *RateCounter, rateCheck time.Duration, reloader *Reloader, received <-chan *ReceivedMessage, sending chan<- OutgoingMessage, done <-chan TerminationRequest, relayAll bool) {

	tick := time.Tick(1 * time.Second)
	rateCheckTick := time.Tick(rateCheck)

	for {
		select {
		case <-tick:
			for _, summary := range buffer.Flush(false) {
				sending <- renderer.Render(summary)
			}
		case <-rateCheckTick:
			// Slide the window, and see if this interval trips the alert.
			exceeded, count := rateCounter.CheckAndAdvance()
			if exceeded {
				// TODO actually send an email here, eventually
				log.Printf("rate limit check exceeded: %d messages", count)
			}
		case msg := <-received:
			buffer.Add(msg)
			rateCounter.Add(1)
			if relayAll {
				sending <- msg
			}
		case req := <-done:
			if req == Reload {
				reloader.RequestReload()
			}
			log.Printf("cleaning up")
			for _, summary := range buffer.Flush(true) {
				sending <- renderer.Render(summary)
			}
			close(sending)
			break
		}
	}
}

func writePidfile(pidfile string) {
	if _, err := os.Stat(pidfile); err != nil && !os.IsNotExist(err) {
		log.Fatalf("could not write pidfile %s: %v", pidfile, err)
	} else if err == nil {
		log.Fatalf("pidfile %s already exists", pidfile)
	}

	if file, err := os.Create(pidfile); err == nil {
		fmt.Fprintf(file, "%d\n", os.Getpid())
		defer file.Close()
	} else {
		log.Fatalf("could not write pidfile %s: %s", pidfile, err)
	}
}
