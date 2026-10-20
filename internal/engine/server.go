package engine

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/movingChurch/runtime-status/internal/adapters"
)

type renderFunc func(map[string]any, int, bool) []string
type subscriber struct {
	run, format string
	width       int
	last        string
	frames      chan []byte
	conn        net.Conn
}
type tickResult struct {
	worker    *collector
	original  map[string]*run
	snapshots map[string]map[string]any
}
type request struct {
	conn         net.Conn
	value        map[string]any
	disconnected bool
}

// Serve owns the collector lock for the complete lifetime of the socket.
func Serve(runtime string) error {
	root, err := ValidateRuntime(runtime)
	if err != nil {
		return err
	}
	lock, err := privateOpen(filepath.Join(root, "collector.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil
		}
		return err
	}
	path := filepath.Join(root, "collector.sock")
	if info, err := os.Lstat(path); err == nil {
		st, ok := info.Sys().(*syscall.Stat_t)
		if info.Mode()&os.ModeSocket == 0 || !ok || st.Uid != uint32(os.Getuid()) {
			return errors.New("unsafe collector socket")
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	return runServer(listener, root, newCollector(productionDependencies()), renderSnapshot, time.Second, time.Second)
}

// All run and subscriber state belongs to this loop; workers only send requests.
func runServer(listener net.Listener, runtime string, c *collector, render renderFunc, interval, idle time.Duration) error {
	workerGrace, configErr := idleWorkerGrace()
	if configErr != nil {
		return configErr
	}
	requests := make(chan request, 64)
	stop := make(chan struct{})
	var workers sync.WaitGroup
	var connections sync.Map
	deliver := func(r request) bool {
		select {
		case requests <- r:
			return true
		case <-stop:
			return false
		}
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Store(conn, true)
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer connections.Delete(conn)
				defer conn.Close()
				defer deliver(request{conn: conn, disconnected: true})
				conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				scanner := bufio.NewScanner(conn)
				scanner.Buffer(make([]byte, 1024), maxMessage)
				for scanner.Scan() {
					var value map[string]any
					if json.Unmarshal(scanner.Bytes(), &value) != nil {
						return
					}
					if !deliver(request{conn: conn, value: value}) {
						return
					}
					conn.SetReadDeadline(time.Time{})
				}
			}()
		}
	}()
	clients := map[net.Conn]*subscriber{}
	callbacks := newCallbackStore()
	book := newAccountBook()
	profiles := map[string]*identityProfile{}
	identityResults := make(chan identityResult, 64)
	profileKey := func(meta map[string]any) string {
		return stringValue(meta["runtime"]) + "\x00" + stringValue(meta["home"]) + "\x00" + stringValue(meta["executable"])
	}
	ensureIdentity := func(id string, meta map[string]any, now time.Time) {
		key := profileKey(meta)
		generation := AuthGeneration(stringValue(meta["runtime"]), stringValue(meta["home"]))
		profile := profiles[key]
		if profile == nil || profile.generation != generation {
			profile = &identityProfile{generation: generation, pending: true}
			profiles[key] = profile
			workers.Add(1)
			go func() {
				defer workers.Done()
				identity := resolveIdentity(meta)
				select {
				case identityResults <- identityResult{key, generation, identity}:
				case <-stop:
				}
			}()
		}
		callbacks.bind(id, stringValue(meta["started"])+generation, profile.identity, now)
	}
	results := make(chan tickResult, 1)
	inFlight := false
	visibilityChanged := false
	headReads := int64(0)
	hadClients := false
	started := time.Now()
	lastEmpty := started
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer func() {
		close(stop)
		listener.Close()
		connections.Range(func(key, value any) bool { key.(net.Conn).Close(); return true })
		for _, client := range clients {
			close(client.frames)
		}
		workers.Wait()
		c.close()
	}()
	remove := func(conn net.Conn) {
		if client := clients[conn]; client != nil {
			delete(clients, conn)
			close(client.frames)
			conn.Close()
			used := false
			for _, other := range clients {
				if other.run == client.run {
					used = true
					break
				}
			}
			if !used && !liveMetadata(c.runs[client.run].meta) {
				delete(c.runs, client.run)
				delete(callbacks.runs, client.run)
				callbacks.registry.Remove(client.run, time.Now())
			}
			if len(clients) == 0 {
				lastEmpty = time.Now()
			}
		}
	}
	for {
		select {
		case event := <-requests:
			if event.disconnected {
				remove(event.conn)
				continue
			}
			if client := clients[event.conn]; client != nil {
				width, ok := displayWidth(event.value["width"])
				if !ok {
					remove(event.conn)
					continue
				}
				client.width = width
				client.last = ""
				continue
			}
			if event.value["v"] != float64(1) {
				event.conn.Close()
				continue
			}
			if event.value["op"] == "status" {
				frames, contexts, quotas, tickets, rejections, pending := 0, 0, 0, 0, 0, 0
				diagnostics := []any{}
				for _, r := range callbacks.runs {
					frames += int(r.updates)
					tickets += int(r.sequence)
					if r.diagnostic == "session-pending" {
						pending++
					}
					if r.diagnostic != "" && r.diagnostic != "session-pending" {
						rejections++
						if r.diagnostics != nil {
							diagnostics = append(diagnostics, r.diagnostics)
						}
					}
					if r.data["context_remaining"] != nil {
						contexts++
					}
					if r.data["weekly"] != nil || r.data["five_hour"] != nil || r.data["monthly"] != nil || r.data["quota_pools"] != nil {
						quotas++
					}
				}
				var usage syscall.Rusage
				_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &usage)
				state := map[string]any{"v": 1, "protocol": 2, "capabilities": []string{"callback-ticket", "adaptive-metrics", "shared-tmux", "hook-activity"}, "started": processIdentity(), "pid": os.Getpid(), "clients": len(clients), "runs": len(c.runs), "metrics": c.metrics, "head_reads": headReads, "children_cpu_seconds": float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6}
				state["callback_frames"] = frames
				state["callback_tickets"] = tickets
				state["callback_rejections"] = rejections
				state["callback_pending_runs"] = pending
				state["callback_diagnostics"] = diagnostics
				state["idle_worker_grace_seconds"] = workerGrace.Seconds()
				state["context_observed_runs"] = contexts
				state["quota_observed_runs"] = quotas
				event.conn.SetWriteDeadline(time.Now().Add(time.Second))
				json.NewEncoder(event.conn).Encode(state)
				event.conn.Close()
				continue
			}
			id := stringValue(event.value["run"])
			meta, err := ReadMetadata(runtime, id)
			if err != nil {
				event.conn.Close()
				continue
			}
			if op := stringValue(event.value["op"]); op == "ticket" || op == "publish" || op == "reject" || op == "pending" || op == "activity" {
				response := map[string]any{"error": "invalid-registration"}
				if liveMetadata(meta) && (meta["runtime"] == "claude" || meta["runtime"] == "antigravity") {
					epoch := stringValue(meta["started"]) + AuthGeneration(stringValue(meta["runtime"]), stringValue(meta["home"]))
					switch op {
					case "ticket":
						ensureIdentity(id, meta, time.Now())
						response = callbacks.ticket(id, epoch, time.Now())
					case "reject":
						if callbacks.reject(id, epoch, stringValue(event.value["ticket"]), time.Now()) {
							callbacks.runs[id].diagnostics, _ = event.value["diagnostics"].(map[string]any)
							response = map[string]any{"v": 1, "accepted": true}
						}
					case "activity":
						activity := stringValue(event.value["activity"])
						known := false
						for _, v := range adapters.HookEvents {
							known = known || v == activity
						}
						if meta["runtime"] == "claude" && known && callbacks.activity(id, epoch, stringValue(event.value["session_id"]), activity) {
							response = map[string]any{"v": 1, "accepted": true}
						}
					case "pending":
						if callbacks.pending(id, epoch, stringValue(event.value["ticket"]), time.Now()) {
							response = map[string]any{"v": 1, "accepted": true}
						}
					default:
						data, _ := event.value["data"].(map[string]any)
						if data != nil && data["runtime"] == meta["runtime"] && callbacks.update(id, epoch, stringValue(event.value["ticket"]), data, time.Now()) {
							response = map[string]any{"v": 1, "accepted": true}
						} else {
							response = map[string]any{"error": "stale-or-incompatible-payload"}
						}
					}
				}
				_ = event.conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
				_ = json.NewEncoder(event.conn).Encode(response)
				_ = event.conn.Close()
				continue
			}
			format := "json"
			if value, ok := event.value["format"]; ok {
				format = stringValue(value)
			}
			width := 80
			if value, ok := event.value["width"]; ok {
				var valid bool
				width, valid = displayWidth(value)
				if !valid {
					event.conn.Close()
					continue
				}
			}
			if format != "json" && format != "ansi" {
				event.conn.Close()
				continue
			}
			if existing := c.runs[id]; existing != nil {
				if !reflect.DeepEqual(existing.meta, meta) {
					event.conn.Close()
					continue
				}
			} else {
				epoch, ok := meta["auth_epoch"].(string)
				if !ok {
					epoch = c.deps.epoch(stringValue(meta["home"]))
				}
				c.runs[id] = &run{meta: meta, epoch: epoch}
				ensureIdentity(id, meta, time.Now())
			}
			client := &subscriber{run: id, format: format, width: width, frames: make(chan []byte, 2), conn: event.conn}
			clients[event.conn] = client
			hadClients = true
			c.visibilityAt = time.Time{}
			if inFlight {
				visibilityChanged = true
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				for frame := range client.frames {
					client.conn.SetWriteDeadline(time.Now().Add(time.Second))
					if _, err := client.conn.Write(frame); err != nil {
						client.conn.Close()
						return
					}
				}
			}()
			client.send(pending(meta), render)
		case identity := <-identityResults:
			profile := profiles[identity.profile]
			if profile == nil || profile.generation != identity.generation {
				continue
			}
			profile.identity = identity.identity
			profile.pending = false
			for id, r := range c.runs {
				if profileKey(r.meta) == identity.profile {
					callbacks.bind(id, stringValue(r.meta["started"])+identity.generation, identity.identity, time.Now())
				}
			}
		case result := <-results:
			inFlight = false
			c.metrics = result.worker.metrics
			c.visible = result.worker.visible
			c.visibilityAt = result.worker.visibilityAt
			if visibilityChanged {
				c.visibilityAt = time.Time{}
				visibilityChanged = false
			}
			headReads = result.worker.branches.reads
			for id, r := range result.worker.runs {
				if c.runs[id] != result.original[id] {
					continue
				}
				c.runs[id] = r
				snapshot := result.snapshots[id]
				merge(snapshot, callbacks.snapshot(id, time.Now()))
				book.annotate(id, r.meta, snapshot, time.Now())
				for _, client := range clients {
					if client.run == id {
						client.send(snapshot, render)
					}
				}
			}
		case now := <-ticker.C:
			for id, r := range c.runs {
				if r.meta["runtime"] == "claude" || r.meta["runtime"] == "antigravity" {
					epoch := stringValue(r.meta["started"]) + AuthGeneration(stringValue(r.meta["runtime"]), stringValue(r.meta["home"]))
					if state := callbacks.runs[id]; state != nil && state.epoch != epoch {
						callbacks.ticket(id, epoch, now)
						ensureIdentity(id, r.meta, now)
					}
				}
			}
			for id, r := range c.runs {
				_, metaErr := ReadMetadata(runtime, id)
				if !liveMetadata(r.meta) || metaErr != nil {
					used := false
					for _, client := range clients {
						if client.run == id {
							used = true
						}
					}
					if !used {
						delete(c.runs, id)
						book.forget(id)
						delete(callbacks.runs, id)
						callbacks.registry.Remove(id, now)
						delete(callbacks.scopes, id)
					}
				}
			}
			for key, ticket := range callbacks.tickets {
				if now.Sub(ticket.issued) > CallbackBudget {
					delete(callbacks.tickets, key)
				}
			}
			for _, key := range callbacks.registry.Sweep(now, workerGrace) {
				delete(callbacks.quota, key)
			}
			if !inFlight && len(c.runs) > 0 {
				inFlight = true
				worker := c.fork()
				original := map[string]*run{}
				for id, r := range c.runs {
					original[id] = r
				}
				workers.Add(1)
				go func() {
					defer workers.Done()
					snapshots := worker.tick(now)
					select {
					case results <- tickResult{worker, original, snapshots}:
					case <-stop:
					}
				}()
			}

			if len(clients) == 0 && len(c.runs) == 0 && (hadClients && now.Sub(lastEmpty) >= idle || !hadClients && now.Sub(started) > 10*time.Second) {
				return nil
			}
		}
	}
}
func displayWidth(value any) (int, bool) {
	width, ok := value.(float64)
	return int(width), ok && width >= 1 && width <= 500 && width == float64(int(width))
}
func (c *subscriber) send(snapshot map[string]any, render renderFunc) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	key := string(raw) + ":" + string(rune(c.width))
	if key == c.last {
		return
	}
	frame := append(raw, '\n')
	if c.format == "ansi" {
		lines := render(snapshot, c.width, true)
		frame = []byte("\x1b[?25l\x1b[H\x1b[2K" + strings.Join(lines, "\r\n\x1b[2K") + "\x1b[J")
	}
	select {
	case c.frames <- frame:
		c.last = key
	default:
	}
}
