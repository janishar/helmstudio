package studio

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/helmstudio/studio_service/api/studio/message"
)

// The admin's unloads (contracts/README.md "Hosts admin": Unload). Each is one req.runner.unload with by: admin to
// one env, sent at once when the env runs nothing, else once its run has ended; repeated every AskEvery while
// unanswered, dropped after AdminUnloadFor. Until every one of a host is answered ok, or UnloadWait after the host
// went idle, the dispatcher releases nothing to that host. In memory: a restart forgets them.

// AdminUnloadFor is how long an admin unload is asked again before it is dropped.
const AdminUnloadFor = 10 * time.Minute

// adminAsk is one admin unload request to one env.
type adminAsk struct {
	host, env      string
	body           []byte
	since, sent    time.Time
	idleAt         time.Time // when a pass first saw the host free while this waited
	inFlight, done bool
}

// AdminUnload queues request for host.env; busy: a run released to the env has not ended (it is asked after it).
func (d *Dispatcher) AdminUnload(host, env string, request message.Unload, busy bool, now time.Time) {
	request.By = "admin"
	body, _ := json.Marshal(request)
	d.mu.Lock()
	defer d.mu.Unlock()
	a := &adminAsk{host: host, env: env, body: body, since: now}
	d.admin = append(d.admin, a)
	if !busy {
		d.adminSend(a, now)
	}
}

// UnloadPending reports whether an admin unload of host.env is not answered yet.
func (d *Dispatcher) UnloadPending(host, env string) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, a := range d.admin {
		if a.host == host && a.env == env && !a.done {
			return true
		}
	}
	return false
}

// adminPass drops answered and expired requests, sends those whose env runs nothing and returns the hosts they hold.
// running: "<host>.<env>" with a released run not ended; busy: hosts a released run holds.
func (d *Dispatcher) adminPass(running, busy map[string]bool, now time.Time) map[string]bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	hold := map[string]bool{}
	kept := d.admin[:0]
	for _, a := range d.admin {
		if a.done {
			continue
		}
		if now.Sub(a.since) > AdminUnloadFor {
			log.Printf("dispatcher: admin unload of %s.%s dropped: unanswered for %s", a.host, a.env, AdminUnloadFor)
			continue
		}
		kept = append(kept, a)
		if !busy[a.host] && a.idleAt.IsZero() {
			a.idleAt = now
		}
		if !running[a.host+"."+a.env] && !a.inFlight && now.Sub(a.sent) >= AskEvery {
			d.adminSend(a, now)
		}
		if a.idleAt.IsZero() || now.Sub(a.idleAt) < UnloadWait {
			hold[a.host] = true
		}
	}
	d.admin = kept
	return hold
}

// adminSend sends a's request (d.mu held); its reply marks it done when ok.
func (d *Dispatcher) adminSend(a *adminAsk, now time.Time) {
	if d.Unload == nil {
		return
	}
	a.inFlight, a.sent = true, now
	log.Printf("dispatcher: admin unload of %s.%s", a.host, a.env)
	d.asking.Add(1)
	go func() {
		defer d.asking.Done()
		ctx, cancel := context.WithTimeout(context.Background(), UnloadWait)
		defer cancel()
		raw, err := d.Unload(ctx, "runner.unload."+a.host+"."+a.env, a.body)
		var reply message.UnloadReply
		if err == nil {
			err = json.Unmarshal(raw, &reply)
		}
		ok := err == nil && reply.UnloadedGB != nil && reply.Resident != nil && reply.Code == ""
		if !ok {
			log.Printf("dispatcher: admin unload of %s.%s not done: %v %s %s", a.host, a.env, err, reply.Code, reply.Error)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		a.inFlight, a.done = false, ok
	}()
}
