// The task-67 reference CRUD application: the MVP acceptance
// demonstration. Handlers render fragments through the nethttp adapter
// (FR-035 opt-in mode selection), emit contract events through the
// generated ghtmxgen emitters (FR-037), and the templates bind every
// URL through route-aware bindings (FR-020/FR-021) — zero hand-written
// htmx glue.
//
// Renaming or deleting any route breaks the build at every binding
// site; main_test.go proves it. Symbol bindings pin the handler name in
// the committed generated code, so a rename breaks `go build`
// immediately; constructor bindings break at the next `ghtmx generate`
// (which watch and CI keep in the loop).
package crud

import (
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"

	"github.com/go-monolith/ghtmx/adapters/nethttp"
	"github.com/go-monolith/ghtmx/ghtmxgen"
)

// Todo is the row model.
type Todo struct {
	ID    string
	Title string
	Done  bool
}

// store is an in-memory todo store with deterministic IDs.
type store struct {
	mu    sync.Mutex
	next  int
	todos map[string]Todo
}

func newStore() *store {
	return &store{todos: map[string]Todo{}}
}

// maxTodos and maxTitleLen bound the in-memory store: the app also
// runs as a public live demo on the docs site, where an unbounded
// store would be a memory-growth surface.
const (
	maxTodos    = 100
	maxTitleLen = 200
)

func (s *store) add(title string) (Todo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.todos) >= maxTodos {
		return Todo{}, false
	}
	s.next++
	t := Todo{ID: strconv.Itoa(s.next), Title: title}
	s.todos[t.ID] = t
	return t, true
}

func (s *store) toggle(id string) (Todo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.todos[id]
	if !ok {
		return Todo{}, false
	}
	t.Done = !t.Done
	s.todos[id] = t
	return t, true
}

func (s *store) remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.todos[id]; !ok {
		return false
	}
	delete(s.todos, id)
	return true
}

// toggleAll implements the TodoMVC rule: if any todo is still
// active, everything becomes done; if all are done, everything
// reopens. Returns how many rows changed.
func (s *store) toggleAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	allDone := true
	for _, t := range s.todos {
		if !t.Done {
			allDone = false
			break
		}
	}
	changed := 0
	for id, t := range s.todos {
		if t.Done == !allDone {
			continue
		}
		t.Done = !allDone
		s.todos[id] = t
		changed++
	}
	return changed
}

// clearCompleted removes every done todo, returning how many.
func (s *store) clearCompleted() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for id, t := range s.todos {
		if t.Done {
			delete(s.todos, id)
			removed++
		}
	}
	return removed
}

func (s *store) list() []Todo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Todo, 0, len(s.todos))
	for _, t := range s.todos {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(out[i].ID)
		b, _ := strconv.Atoi(out[j].ID)
		return a < b
	})
	return out
}

func (s *store) rename(id, title string) (Todo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.todos[id]
	if !ok {
		return Todo{}, false
	}
	t.Title = title
	s.todos[id] = t
	return t, true
}

func (s *store) get(id string) (Todo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.todos[id]
	return t, ok
}

func (s *store) stats() (total, done int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statsLocked()
}

func (s *store) statsLocked() (total, done int) {
	for _, t := range s.todos {
		total++
		if t.Done {
			done++
		}
	}
	return total, done
}

// snapshot returns the list and stats under one lock, so a page render
// never shows a list inconsistent with its stats line.
func (s *store) snapshot() (list []Todo, total, done int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.todos {
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool {
		a, _ := strconv.Atoi(list[i].ID)
		b, _ := strconv.Atoi(list[j].ID)
		return a < b
	})
	total, done = s.statsLocked()
	return list, total, done
}

var todos = newStore()

// Template helpers.
func itoa(n int) string { return strconv.Itoa(n) }

func doneClass(done bool) string {
	if done {
		return "done"
	}
	return "pending"
}

// Index serves the full page.
func Index(w http.ResponseWriter, r *http.Request) {
	list, total, done := todos.snapshot()
	if err := todoPage(list, total, done).Render(r.Context(), w); err != nil {
		log.Printf("render index: %v", err)
	}
}

// filterTodos narrows a list to the ?filter= view; anything but
// "active" or "completed" means everything.
func filterTodos(list []Todo, filter string) []Todo {
	if filter != "active" && filter != "completed" {
		return list
	}
	wantDone := filter == "completed"
	out := make([]Todo, 0, len(list))
	for _, t := range list {
		if t.Done == wantDone {
			out = append(out, t)
		}
	}
	return out
}

// ListTodos is the FR-035 demonstration: a browser navigating to
// /todos gets the full page, an htmx refresh gets the bare list — the
// adapter selects the mode from the request. The filter buttons call
// the same bound route with hx-vals, so the view narrows without any
// hand-written URL.
func ListTodos(w http.ResponseWriter, r *http.Request) {
	list, total, done := todos.snapshot()
	list = filterTodos(list, r.FormValue("filter"))
	err := nethttp.Render(w, r,
		nethttp.WithPage(todoPage(list, total, done), todoListFragment(list)))
	if err != nil {
		log.Printf("render list: %v", err)
	}
}

// ToggleAllTodos flips the whole list at once and announces the bulk
// change through one merged event.
func ToggleAllTodos(w http.ResponseWriter, r *http.Request) {
	changed := todos.toggleAll()
	if err := ghtmxgen.EmitTodosBulkChanged(w, ghtmxgen.TodosBulkChangedPayload{Count: changed}); err != nil {
		log.Printf("emit todos-bulk-changed: %v", err)
	}
	list, _, _ := todos.snapshot()
	if err := nethttp.Render(w, r, todoListFragment(list)); err != nil {
		log.Printf("render list: %v", err)
	}
}

// ClearCompleted deletes every done todo and returns the refreshed
// list.
func ClearCompleted(w http.ResponseWriter, r *http.Request) {
	removed := todos.clearCompleted()
	if err := ghtmxgen.EmitTodosBulkChanged(w, ghtmxgen.TodosBulkChangedPayload{Count: removed}); err != nil {
		log.Printf("emit todos-bulk-changed: %v", err)
	}
	list, _, _ := todos.snapshot()
	if err := nethttp.Render(w, r, todoListFragment(list)); err != nil {
		log.Printf("render list: %v", err)
	}
}

// CreateTodo adds a todo, announces it through the event contract, and
// returns the refreshed list fragment.
func CreateTodo(w http.ResponseWriter, r *http.Request) {
	title := r.FormValue("title")
	if title == "" {
		http.Error(w, "title is required", http.StatusUnprocessableEntity)
		return
	}
	if len(title) > maxTitleLen {
		http.Error(w, "title is too long", http.StatusUnprocessableEntity)
		return
	}
	t, ok := todos.add(title)
	if !ok {
		http.Error(w, "the demo store is full — delete a todo first", http.StatusUnprocessableEntity)
		return
	}
	if err := ghtmxgen.EmitTodoCreated(w, ghtmxgen.TodoCreatedPayload{Id: t.ID}); err != nil {
		log.Printf("emit todo-created: %v", err)
	}
	list, _, _ := todos.snapshot()
	err := nethttp.Render(w, r, todoListFragment(list), nethttp.Status(http.StatusCreated))
	if err != nil {
		log.Printf("render list: %v", err)
	}
}

// ToggleTodo flips a todo's done state and returns its refreshed row.
func ToggleTodo(w http.ResponseWriter, r *http.Request) {
	t, ok := todos.toggle(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := ghtmxgen.EmitTodoToggled(w, ghtmxgen.TodoToggledPayload{Id: t.ID, Done: t.Done}); err != nil {
		log.Printf("emit todo-toggled: %v", err)
	}
	if err := nethttp.Render(w, r, todoRowFragment(t)); err != nil {
		log.Printf("render row: %v", err)
	}
}

// EditTodo swaps a row for its inline edit form.
func EditTodo(w http.ResponseWriter, r *http.Request) {
	t, ok := todos.get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := nethttp.Render(w, r, todoEditRowFragment(t)); err != nil {
		log.Printf("render edit row: %v", err)
	}
}

// RenameTodo saves an edited title and swaps the row back.
func RenameTodo(w http.ResponseWriter, r *http.Request) {
	title := r.FormValue("title")
	if title == "" {
		http.Error(w, "title is required", http.StatusUnprocessableEntity)
		return
	}
	if len(title) > maxTitleLen {
		http.Error(w, "title is too long", http.StatusUnprocessableEntity)
		return
	}
	t, ok := todos.rename(r.PathValue("id"), title)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := nethttp.Render(w, r, todoRowFragment(t)); err != nil {
		log.Printf("render row: %v", err)
	}
}

// DeleteTodo removes a todo; the empty response with an outerHTML swap
// removes the row client-side.
func DeleteTodo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !todos.remove(id) {
		http.NotFound(w, r)
		return
	}
	if err := ghtmxgen.EmitTodoDeleted(w, ghtmxgen.TodoDeletedPayload{Id: id}); err != nil {
		log.Printf("emit todo-deleted: %v", err)
	}
	w.WriteHeader(http.StatusOK)
}

// TodoStats renders the stats panel fragment.
func TodoStats(w http.ResponseWriter, r *http.Request) {
	total, done := todos.stats()
	if err := nethttp.Render(w, r, statsPanelFragment(total, done)); err != nil {
		log.Printf("render stats: %v", err)
	}
}

// Routes builds the example's router; the official docs site mounts
// it as a live demo.
func Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", Index)
	mux.HandleFunc("GET /todos", ListTodos)
	mux.HandleFunc("POST /todos", CreateTodo)
	mux.HandleFunc("GET /todos/{id}/edit", EditTodo)
	mux.HandleFunc("PUT /todos/{id}/title", RenameTodo)
	mux.HandleFunc("PUT /todos/{id}", ToggleTodo)
	mux.HandleFunc("PUT /todos/toggle-all", ToggleAllTodos)
	mux.HandleFunc("DELETE /todos/{id}", DeleteTodo)
	mux.HandleFunc("DELETE /todos/completed", ClearCompleted)
	mux.HandleFunc("GET /todos/stats", TodoStats)
	return mux
}
