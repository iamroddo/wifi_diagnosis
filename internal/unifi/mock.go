package unifi

import (
	"context"
	"sync"
	"time"
)

// MockClient is a deterministic UniFi client for development and testing.
// It is safe for concurrent use.
type MockClient struct {
	mu          sync.RWMutex
	clients     []Client
	unavailable bool
}

// NewMockClient creates a MockClient pre-populated with a set of fake clients.
func NewMockClient() *MockClient {
	return &MockClient{
		clients: []Client{
			{
				MAC:          "aa:bb:cc:dd:ee:01",
				IP:           "192.168.20.47",
				Hostname:     "iPhone-Rod",
				Name:         "Rod's iPhone",
				ESSID:        "Home",
				APMAC:        "78:45:58:aa:bb:01",
				Band:         "5G",
				Channel:      44,
				ChannelWidth: "80",
				RSSI:         -67,
				TXRate:       866000,
				RXRate:       780000,
				Retries:      7,
				Satisfaction: 92,
				LastSeen:     time.Now(),
			},
			{
				MAC:          "aa:bb:cc:dd:ee:02",
				IP:           "192.168.20.48",
				Hostname:     "MacBook-Pro",
				Name:         "Rod's MacBook",
				ESSID:        "Home",
				APMAC:        "78:45:58:aa:bb:01",
				Band:         "5G",
				Channel:      44,
				ChannelWidth: "80",
				RSSI:         -54,
				TXRate:       1300000,
				RXRate:       1300000,
				Retries:      1,
				Satisfaction: 98,
				LastSeen:     time.Now(),
			},
		},
	}
}

// SetUnavailable controls whether the mock simulates a controller outage.
func (m *MockClient) SetUnavailable(v bool) {
	m.mu.Lock()
	m.unavailable = v
	m.mu.Unlock()
}

// AddClient adds a client to the mock, useful in tests.
func (m *MockClient) AddClient(cl Client) {
	m.mu.Lock()
	m.clients = append(m.clients, cl)
	m.mu.Unlock()
}

// SetClients replaces the full client list.
func (m *MockClient) SetClients(clients []Client) {
	m.mu.Lock()
	m.clients = clients
	m.mu.Unlock()
}

// FindClientByIP implements UniFiClient.
func (m *MockClient) FindClientByIP(_ context.Context, ip string) (*Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.unavailable {
		return nil, &UnavailableError{}
	}

	var matches []Client
	for _, cl := range m.clients {
		if cl.IP == ip {
			matches = append(matches, cl)
		}
	}
	switch len(matches) {
	case 0:
		return nil, &NotFoundError{IP: ip}
	case 1:
		cl := matches[0]
		return &cl, nil
	default:
		return nil, &AmbiguousError{IP: ip, Count: len(matches)}
	}
}

// Ping implements UniFiClient.
func (m *MockClient) Ping(_ context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.unavailable {
		return &UnavailableError{}
	}
	return nil
}
