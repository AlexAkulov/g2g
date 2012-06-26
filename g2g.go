package g2g

import (
	"fmt"
	"time"
	"net"
	"expvar"
	"github.com/peterbourgon/bonus/xlog"
)

// Graphite represents a Graphite server. You Register expvars
// in this struct, which will be published to the server on a
// regular interval.
type Graphite struct {
	endpoint      string
	interval      time.Duration
	timeout       time.Duration
	connection    net.Conn
	vars          map[string]expvar.Var
	registrations chan namedVar
	shutdown      chan chan bool
}

type namedVar struct {
	name string
	v    expvar.Var
}

// NewGraphite returns a Graphite structure with an open and working
// connection, but no active/registered variables being published.
// Endpoint should be of the format "host:port", eg. "stats:2003".
// Interval is the (best-effort) minimum duration between (sequential)
// publishments of Registered expvars. Timeout is per-publish-action.
func NewGraphite(endpoint string, interval, timeout time.Duration) (*Graphite, error) {
	g := &Graphite{
		endpoint:      endpoint,
		interval:      interval,
		timeout:       timeout,
		connection:    nil,
		vars:          map[string]expvar.Var{},
		registrations: make(chan namedVar),
		shutdown:      make(chan chan bool),
	}
	if err := g.reconnect(); err != nil {
		return nil, err
	}
	go g.loop()
	return g, nil
}

// Register registers an expvar under the given name. (Roughly) every
// interval, the current value of the given expvar will be published to
// Graphite under the given name.
func (g *Graphite) Register(name string, v expvar.Var) {
	g.registrations <- namedVar{name, v}
}

// Shutdown signals the Graphite structure to stop publishing
// Registered expvars.
func (g *Graphite) Shutdown() {
	q := make(chan bool)
	g.shutdown <- q
	<-q
}

func (g *Graphite) loop() {
	for {
		select {
		case nv := <-g.registrations:
			g.vars[nv.name] = nv.v
		case <-time.After(g.interval):
			xlog.Infof("Graphite: publishing all")
			g.postAll()
		case q := <-g.shutdown:
			g.connection.Close()
			g.connection = nil
			q <- true
			return
		}
	}
}

// postAll publishes all Registered expvars to the Graphite server.
func (g *Graphite) postAll() {
	for name, v := range g.vars {
		if err := g.postOne(name, v.String()); err != nil {
			xlog.Problemf("Graphite: %s: %s", name, err)
		}
	}
}

// postOne publishes the given name-value pair to the Graphite server.
// If the connection fails or is failed, one reconnect attempt is made.
func (g *Graphite) postOne(name, value string) error {
	if g.connection == nil {
		if err := g.reconnect(); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(g.timeout)
	if err := g.connection.SetWriteDeadline(deadline); err != nil {
		return err
	}
	s := fmt.Sprintf("%s %s %d\n", name, value, time.Now().Unix())
	b := []byte(s)
	if n, err := g.connection.Write(b); err != nil {
		return err
	} else if n != len(b) {
		return fmt.Errorf("%s = %v: short write: %d/%d", name, value, n, len(b))
	}
	return nil
}

// reconnect attempts to (re-)establish a TCP connection to the Graphite server.
func (g *Graphite) reconnect() error {
	conn, err := net.Dial("tcp", g.endpoint)
	if err != nil {
		return err
	}
	g.connection = conn
	return nil
}