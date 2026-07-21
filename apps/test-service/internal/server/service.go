package server

import (
	"net"
	"sync"

	"unit-test-ide.local/test-service/internal/session"
)

type ServiceConfig struct {
	MaxConnections int
	Connection     ConnectionConfig
}

type Service struct {
	listener                   net.Listener
	token, platform, transport string
	config                     ServiceConfig
	stop                       chan struct{}
	stopOnce                   sync.Once
	mu                         sync.Mutex
	connections                map[net.Conn]struct{}
	handlers                   sync.WaitGroup
}

func NewService(listener net.Listener, token, platform, transport string, config ServiceConfig) *Service {
	if config.MaxConnections <= 0 {
		config.MaxConnections = 64
	}
	if config.Connection == (ConnectionConfig{}) {
		config.Connection = DefaultConnectionConfig
	}
	return &Service{
		listener: listener, token: token, platform: platform, transport: transport,
		config: config, stop: make(chan struct{}), connections: make(map[net.Conn]struct{}),
	}
}

func (s *Service) Serve() error {
	capacity := make(chan struct{}, s.config.MaxConnections)
	defer func() {
		s.Shutdown()
		s.handlers.Wait()
	}()
	for {
		select {
		case capacity <- struct{}{}:
		case <-s.stop:
			return nil
		}
		connection, err := s.listener.Accept()
		if err != nil {
			<-capacity
			select {
			case <-s.stop:
				return nil
			default:
			}
			return err
		}
		s.mu.Lock()
		s.connections[connection] = struct{}{}
		s.mu.Unlock()
		s.handlers.Add(1)
		go func() {
			defer s.handlers.Done()
			defer func() { <-capacity }()
			active := session.New(s.token, s.platform, s.transport)
			ServeConnectionWithConfig(connection, active, s.config.Connection)
			s.mu.Lock()
			delete(s.connections, connection)
			s.mu.Unlock()
			select {
			case <-active.ShutdownRequested():
				s.Shutdown()
			default:
			}
		}()
	}
}

func (s *Service) Shutdown() {
	s.stopOnce.Do(func() {
		close(s.stop)
		_ = s.listener.Close()
		s.mu.Lock()
		for connection := range s.connections {
			_ = connection.Close()
		}
		s.mu.Unlock()
	})
}
