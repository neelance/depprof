package depprof

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type handler struct {
	filterPrefix string
	deps         map[[2]string]struct{}
	depsMutex    sync.Mutex
}

func NewHandler(filterPrefix string) http.Handler {
	h := &handler{
		filterPrefix: filterPrefix,
		deps:         make(map[[2]string]struct{}),
	}
	go h.recordLoop()
	return h
}

var filterPrefix = "sourcegraph.com/sourcegraph/sourcegraph/"
var deps = make(map[[2]string]struct{})
var depsMutex sync.Mutex

func (h *handler) recordLoop() {
	for {
		h.recordStacks()
		time.Sleep(100 * time.Millisecond)
	}
}

func (h *handler) recordStacks() {
	var p []runtime.StackRecord
	n, _ := runtime.GoroutineProfile(nil)
	for {
		p = make([]runtime.StackRecord, n+10)
		n2, ok := runtime.GoroutineProfile(p)
		if ok {
			p = p[:n2]
			break
		}
		n = n2 // try again
	}

	h.depsMutex.Lock()
	defer h.depsMutex.Unlock()

	for _, sr := range p {
		prevPkg := ""
		for _, pc := range sr.Stack() {
			file, _ := runtime.FuncForPC(pc).FileLine(pc)
			pkg, ok := fileToPkg(file)
			if !ok || !strings.HasPrefix(pkg, h.filterPrefix) {
				continue
			}
			if prevPkg != "" && pkg != prevPkg {
				h.deps[[2]string{pkg, prevPkg}] = struct{}{}
			}
			prevPkg = pkg
		}
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("show") {
	case "graph":
		h.depsMutex.Lock()
		defer h.depsMutex.Unlock()

		cmd := exec.Command("dot", "-Tsvg")
		cmd.Stdout = w
		cmd.Stderr = os.Stderr
		in, err := cmd.StdinPipe()
		if err != nil {
			panic(err)
		}

		go func() {
			defer in.Close()

			fmt.Fprintf(in, "digraph g {")
			for dep := range h.deps {
				fmt.Fprintf(in, `"%s" -> "%s";`, strings.TrimPrefix(dep[0], h.filterPrefix), strings.TrimPrefix(dep[1], h.filterPrefix))
			}
			fmt.Fprintf(in, "}")
		}()

		if err := cmd.Run(); err != nil {
			panic(err)
		}

	default:
		w.Write([]byte(`<a href="?show=graph">Graph</a>`))

	}
}

func fileToPkg(file string) (string, bool) {
	i := strings.Index(file, "/src/")
	if i == -1 {
		return "", false
	}
	j := strings.LastIndexByte(file, '/')
	return file[i+5 : j], true
}
