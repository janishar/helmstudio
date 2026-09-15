package supervisor

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// Default port range for assigned ports (docs/design/01-prd.md R22).
const (
	DefaultPortMin = 8701
	DefaultPortMax = 8799
)

// portFree reports whether nothing listens on port on loopback. Binding alone
// is not enough: with SO_REUSEADDR a bind can succeed beside a listener on
// another address, so a connect is tried as well.
func portFree(port int) bool {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	l.Close()
	c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err == nil {
		c.Close()
		return false
	}
	return true
}

// allocator hands out ports. Leases are in memory: processes.port records
// what was used, and a restarted daemon re-leases the ports of the processes
// it re-adopts (docs/design/02-data-model.md §1, PortLease is derived).
type allocator struct {
	mu       sync.Mutex
	min, max int
	leases   map[int]string // port -> "studio-id/process"
	free     func(port int) bool
	holder   func(port int) string
}

// lease records a port as taken without probing it: a re-adopted process is
// already listening on it.
func (a *allocator) lease(port int, owner string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.leases[port] = owner
}

func (a *allocator) release(port int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.leases, port)
}

// allocate picks a port for one process. fixed must be exactly that port;
// prefer is used when available; otherwise the first available port in range.
func (a *allocator) allocate(owner string, prefer, fixed int) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if fixed != 0 {
		if who, ok := a.leases[fixed]; ok {
			return 0, &Error{Kind: KindPortConflict, Message: fmt.Sprintf(
				"%s needs fixed port %d, which is held by %s; stop that studio first", owner, fixed, who)}
		}
		if !a.free(fixed) {
			who := a.holder(fixed)
			if who == "" {
				who = "a process lsof could not name"
			}
			return 0, &Error{Kind: KindPortConflict, Message: fmt.Sprintf(
				"%s needs fixed port %d, which is held by %s; stop that process, or change the manifest if the port is not really fixed", owner, fixed, who)}
		}
		a.leases[fixed] = owner
		return fixed, nil
	}
	if prefer != 0 {
		if _, taken := a.leases[prefer]; !taken && a.free(prefer) {
			a.leases[prefer] = owner
			return prefer, nil
		}
	}
	for p := a.min; p <= a.max; p++ {
		if _, taken := a.leases[p]; taken {
			continue
		}
		if a.free(p) {
			a.leases[p] = owner
			return p, nil
		}
	}
	return 0, &Error{Kind: KindPortConflict, Message: fmt.Sprintf(
		"%s needs a port, and every port from %d to %d is in use", owner, a.min, a.max)}
}
