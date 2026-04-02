package bot

import "sync"

type stateKind int

const (
	stateNone stateKind = iota
	stateAwaitingOrgName // ожидаем название новой организации для привязки задачи
)

type userState struct {
	kind    stateKind
	payload string // контекст состояния (например, имя задачи)
}

type stateStore struct {
	mu     sync.Mutex
	states map[int64]userState
}

func newStateStore() *stateStore {
	return &stateStore{states: make(map[int64]userState)}
}

func (s *stateStore) set(userID int64, state userState) {
	s.mu.Lock()
	s.states[userID] = state
	s.mu.Unlock()
}

func (s *stateStore) get(userID int64) userState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[userID]
}

func (s *stateStore) clear(userID int64) {
	s.mu.Lock()
	delete(s.states, userID)
	s.mu.Unlock()
}
