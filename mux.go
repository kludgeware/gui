package gui

import (
	"image"
	"image/draw"
	"sync"
)

type Mux struct {
	mu        sync.Mutex
	haveR     bool
	r         image.Rectangle
	eventsIns []chan<- Event
	draw      chan<- func(draw.Image) image.Rectangle
}

func NewMux(env Env) (mux *Mux, master Env) {
	drawChan := make(chan func(draw.Image) image.Rectangle)
	mux = &Mux{draw: drawChan}
	master = mux.makeEnv(true)

	go func() {
		for d := range drawChan {
			env.Draw() <- d
		}
		close(env.Draw())
	}()

	go func() {
		for e := range env.Events() {
			var x0, y0, x1, y1 int
			if e.Matches("resize/%d/%d/%d/%d", &x0, &y0, &x1, &y1) {
				mux.mu.Lock()
				mux.r = image.Rect(x0, y0, x1, y1)
				mux.haveR = true
				mux.mu.Unlock()
			}
			mux.mu.Lock()
			for _, eventsIn := range mux.eventsIns {
				eventsIn <- e
			}
			mux.mu.Unlock()
		}
		mux.mu.Lock()
		for _, eventsIn := range mux.eventsIns {
			close(eventsIn)
		}
		mux.mu.Unlock()
	}()

	return mux, master
}

func (mux *Mux) MakeEnv() Env {
	return mux.makeEnv(false)
}

type muxEnv struct {
	events <-chan Event
	draw   chan<- func(draw.Image) image.Rectangle
}

func (m *muxEnv) Events() <-chan Event                          { return m.events }
func (m *muxEnv) Draw() chan<- func(draw.Image) image.Rectangle { return m.draw }

func (mux *Mux) makeEnv(master bool) Env {
	eventsOut, eventsIn := MakeEventsChan()
	drawChan := make(chan func(draw.Image) image.Rectangle)
	env := &muxEnv{eventsOut, drawChan}

	mux.mu.Lock()
	mux.eventsIns = append(mux.eventsIns, eventsIn)
	// make sure to always send a resize event to a new Env if we got it already
	if mux.haveR {
		eventsIn <- Eventf("resize/%d/%d/%d/%d", mux.r.Min.X, mux.r.Min.Y, mux.r.Max.X, mux.r.Max.Y)
	}
	mux.mu.Unlock()

	go func() {
		func() {
			// When the master Env gets its Draw() channel closed, it closes all the Events()
			// channels of all the children Envs, and it also closes the internal draw channel
			// of the Mux. Otherwise, closing the Draw() channel of the master Env wouldn't
			// close the Env the Mux is muxing. However, some child Envs of the Mux may still
			// send some drawing commmands before they realize that their Events() channel got
			// closed.
			//
			// That is perfectly fine if their drawing commands simply get ignored. This down here
			// is a little hacky, but (I hope) perfectly fine solution to the problem.
			//
			// When the internal draw channel of the Mux gets closed, the line marked with ! will
			// cause panic. We recover this panic, then we receive, but ignore all furhter draw
			// commands, correctly draining the Env until it closes itself.
			defer func() {
				if recover() != nil {
					for range drawChan {
					}
				}
			}()
			for d := range drawChan {
				mux.draw <- d // !
			}
		}()
		if master {
			mux.mu.Lock()
			for _, eventsIn := range mux.eventsIns {
				close(eventsIn)
			}
			mux.eventsIns = nil
			close(mux.draw)
			mux.mu.Unlock()
		} else {
			mux.mu.Lock()
			i := -1
			for i = range mux.eventsIns {
				if mux.eventsIns[i] == eventsIn {
					break
				}
			}
			if i != -1 {
				mux.eventsIns = append(mux.eventsIns[:i], mux.eventsIns[i+1:]...)
			}
			mux.mu.Unlock()
		}
	}()

	return env
}
