// Package stats keeps the agent's request and byte counters.
//
// What they are for is the information the AGENT's owner sees, not the
// visitor's: how many requests reached which target, how many failed, how
// much data moved. The measurements on the connect screen are a separate
// thing — the browser counts those itself.
//
// The counters are atomic: requests are handled on different goroutines
// per session (tunnel.Session drives every request on its own
// goroutine), so we use sync/atomic rather than a lock.
package stats

import (
	"sort"
	"sync/atomic"
)

// Snapshot is one reading.
type Snapshot struct {
	// Requests is the number of requests started.
	Requests uint64
	// Success is the number of requests whose response was carried in
	// full.
	//
	// The target answering 404 or 500 IS a success: the proxy did its job.
	// Errors only counts the cases where the agent could not carry the
	// response.
	Success uint64
	// Errors: the target was unreachable, the path was invalid, or the
	// transfer was cut off halfway.
	Errors uint64
	// BytesIn is the request body bytes coming from the visitor.
	BytesIn uint64
	// BytesOut is the response body bytes going to the visitor.
	//
	// The BODY only: the JSON messages on the ctrl channel and the frame
	// headers are not counted, so the real traffic on the wire is somewhat
	// more than this.
	BytesOut uint64
}

// Counters are the counters of a single target (or of the total).
type Counters struct {
	requests atomic.Uint64
	success  atomic.Uint64
	errors   atomic.Uint64
	bytesIn  atomic.Uint64
	bytesOut atomic.Uint64

	// parent is the total counter; it may be nil (on the total itself).
	parent *Counters
}

// AddRequest counts a new request.
func (c *Counters) AddRequest() {
	if c == nil {
		return
	}
	c.requests.Add(1)
	c.parent.AddRequest()
}

// AddSuccess counts a request whose response was carried in full.
func (c *Counters) AddSuccess() {
	if c == nil {
		return
	}
	c.success.Add(1)
	c.parent.AddSuccess()
}

// AddError counts a request that could not be carried.
func (c *Counters) AddError() {
	if c == nil {
		return
	}
	c.errors.Add(1)
	c.parent.AddError()
}

// AddBytesIn adds the body bytes coming from the visitor.
func (c *Counters) AddBytesIn(n int64) {
	if c == nil || n <= 0 {
		return
	}
	c.bytesIn.Add(uint64(n))
	c.parent.AddBytesIn(n)
}

// AddBytesOut adds the body bytes going to the visitor.
func (c *Counters) AddBytesOut(n int64) {
	if c == nil || n <= 0 {
		return
	}
	c.bytesOut.Add(uint64(n))
	c.parent.AddBytesOut(n)
}

// Snapshot returns a copy of the counters as they are right now.
//
// The fields are read one by one, so under heavy load they may not be
// exactly consistent with each other (requests may have gone up while
// success has not yet). That is deliberate: taking a lock would slow the
// hot path down, and the numbers shown correct themselves on the next
// tick.
func (c *Counters) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	return Snapshot{
		Requests: c.requests.Load(),
		Success:  c.success.Load(),
		Errors:   c.errors.Load(),
		BytesIn:  c.bytesIn.Load(),
		BytesOut: c.bytesOut.Load(),
	}
}

// Registry holds the per-target counters and the total.
//
// The target list is fixed when the agent starts (it comes from the CLI),
// so once the map is built it is read-only: no lock needed.
type Registry struct {
	total  *Counters
	byName map[string]*Counters
	names  []string
}

// New builds the counter set for the given target names.
func New(names []string) *Registry {
	r := &Registry{
		total:  &Counters{},
		byName: make(map[string]*Counters, len(names)),
	}
	r.names = append(r.names, names...)
	sort.Strings(r.names)
	for _, n := range r.names {
		r.byName[n] = &Counters{parent: r.total}
	}
	return r
}

// Target returns a target's counters.
//
// For an unknown name it returns nil; because the Counters methods are
// nil-safe the caller does not have to check (an unknown target is
// already refused in the connect layer).
func (r *Registry) Target(name string) *Counters {
	if r == nil {
		return nil
	}
	return r.byName[name]
}

// Total is the total counters.
func (r *Registry) Total() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	return r.total.Snapshot()
}

// Names are the target names (alphabetical).
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	return r.names
}

// Snapshot is the per-target reading.
func (r *Registry) Snapshot() map[string]Snapshot {
	if r == nil {
		return nil
	}
	out := make(map[string]Snapshot, len(r.byName))
	for name, c := range r.byName {
		out[name] = c.Snapshot()
	}
	return out
}
