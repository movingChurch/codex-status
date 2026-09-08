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
			if !used {
				delete(c.runs, client.run)
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
				var usage syscall.Rusage
				_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &usage)
				state := map[string]any{"v": 1, "pid": os.Getpid(), "clients": len(clients), "runs": len(c.runs), "metrics": c.metrics, "head_reads": headReads, "children_cpu_seconds": float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6}
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
				for _, client := range clients {
					if client.run == id {
						client.send(result.snapshots[id], render)
					}
				}
			}
		case now := <-ticker.C:
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

			if len(clients) == 0 && (hadClients && now.Sub(lastEmpty) >= idle || !hadClients && now.Sub(started) > 10*time.Second) {
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
