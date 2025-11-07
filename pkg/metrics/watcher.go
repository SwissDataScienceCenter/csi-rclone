package metrics

import "time"

type Observable func()

type Watcher struct {
	ticker     *time.Ticker
	tickerStop chan bool

	observe Observable
}

func (watcher *Watcher) Poll(observe Observable, every time.Duration) {
	watcher.ticker = time.NewTicker(every)
	watcher.tickerStop = make(chan bool)
	watcher.observe = observe

	go watcher.tick()
}

func (watcher *Watcher) tick() {
	defer watcher.ticker.Stop()

	for {
		select {
		case <-watcher.tickerStop:
			return
		case <-watcher.ticker.C:
			watcher.observe()
		}
	}
}

func (watcher *Watcher) Stop() {
	watcher.tickerStop <- true
}
