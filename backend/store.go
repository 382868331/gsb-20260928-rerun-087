package main

import (
	"encoding/json"
	"os"
	"sync"
)

type Persisted struct {
	Projects map[string]Project `json:"projects"`
	Runs     map[string]Run     `json:"runs"`
}

type Store struct {
	mu       sync.Mutex
	path     string
	Projects map[string]Project
	Runs     map[string]Run
}

func NewStore(path string) *Store {
	return &Store{path: path, Projects: map[string]Project{}, Runs: map[string]Run{}}
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var p Persisted
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if p.Projects != nil {
		s.Projects = p.Projects
	}
	if p.Runs != nil {
		s.Runs = p.Runs
	}
	return nil
}

func (s *Store) SaveLocked() error {
	p := Persisted{Projects: s.Projects, Runs: s.Runs}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0644)
}
