package unifi

import (
	"encoding/json"
	"testing"
)

var sampleStaResponse = []byte(`{
  "meta": {"rc": "ok"},
  "data": [
    {
      "mac": "aa:bb:cc:dd:ee:01",
      "ip": "192.168.20.47",
      "hostname": "iphone-rod",
      "name": "Rod iPhone",
      "essid": "Home",
      "ap_mac": "78:45:58:aa:bb:01",
      "band": "5G",
      "channel": 44,
      "channel_width": "80",
      "rssi": -67,
      "tx_rate": 866000,
      "rx_rate": 780000,
      "retries": 7,
      "satisfaction": 92,
      "is_wired": false
    },
    {
      "mac": "aa:bb:cc:dd:ee:02",
      "ip": "192.168.20.48",
      "hostname": "macbook",
      "is_wired": true
    }
  ]
}`)

func TestParseStaResponse(t *testing.T) {
	clients, err := parseStaResponse(sampleStaResponse)
	if err != nil {
		t.Fatalf("parseStaResponse: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("want 2 clients, got %d", len(clients))
	}

	cl := clients[0]
	if cl.MAC != "aa:bb:cc:dd:ee:01" {
		t.Errorf("MAC: got %q", cl.MAC)
	}
	if cl.IP != "192.168.20.47" {
		t.Errorf("IP: got %q", cl.IP)
	}
	if cl.ESSID != "Home" {
		t.Errorf("ESSID: got %q", cl.ESSID)
	}
	if cl.Band != "5G" {
		t.Errorf("Band: got %q", cl.Band)
	}
	if cl.Channel != 44 {
		t.Errorf("Channel: got %d", cl.Channel)
	}
	if cl.RSSI != -67 {
		t.Errorf("RSSI: got %d", cl.RSSI)
	}
	if cl.TXRate != 866000 {
		t.Errorf("TXRate: got %d", cl.TXRate)
	}
	if cl.Retries != 7 {
		t.Errorf("Retries: got %d", cl.Retries)
	}
	if cl.IsWired {
		t.Error("IsWired: want false")
	}

	wired := clients[1]
	if !wired.IsWired {
		t.Error("IsWired: want true for second client")
	}
}

func TestParseStaResponse_SignalFallback(t *testing.T) {
	// Some firmware versions use "signal" instead of "rssi".
	data := []byte(`{"meta":{"rc":"ok"},"data":[{"mac":"aa:bb:cc:00:00:01","ip":"10.0.0.1","signal":-72}]}`)
	clients, err := parseStaResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	if clients[0].RSSI != -72 {
		t.Errorf("RSSI fallback from signal: got %d, want -72", clients[0].RSSI)
	}
}

func TestParseStaResponse_ExtraFields(t *testing.T) {
	// Unknown fields should not cause parse errors; they end up in RawFields.
	data := []byte(`{"meta":{"rc":"ok"},"data":[{"mac":"aa:bb:cc:00:00:02","ip":"10.0.0.2","future_field":"xyz"}]}`)
	clients, err := parseStaResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	if clients[0].RawFields["future_field"] != "xyz" {
		t.Error("expected future_field in RawFields")
	}
}

func TestMockClient(t *testing.T) {
	m := NewMockClient()

	ctx := t.Context()

	cl, err := m.FindClientByIP(ctx, "192.168.20.47")
	if err != nil {
		t.Fatalf("FindClientByIP: %v", err)
	}
	if cl.MAC != "aa:bb:cc:dd:ee:01" {
		t.Errorf("MAC: got %q", cl.MAC)
	}

	_, err = m.FindClientByIP(ctx, "99.99.99.99")
	if !IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}

	m.SetUnavailable(true)
	_, err = m.FindClientByIP(ctx, "192.168.20.47")
	if !IsUnavailable(err) {
		t.Errorf("expected Unavailable, got %v", err)
	}
}

func TestClientDisplayName(t *testing.T) {
	cases := []struct {
		cl   Client
		want string
	}{
		{Client{Name: "Rod's iPhone", Hostname: "iphone", MAC: "aa:bb"}, "Rod's iPhone"},
		{Client{DeviceName: "laptop", Hostname: "mbp", MAC: "cc:dd"}, "laptop"},
		{Client{Hostname: "myhost", MAC: "ee:ff"}, "myhost"},
		{Client{MAC: "11:22"}, "11:22"},
	}
	for _, tc := range cases {
		got := tc.cl.DisplayName()
		if got != tc.want {
			t.Errorf("DisplayName() = %q, want %q", got, tc.want)
		}
	}
}

// Ensure Client is JSON-round-trippable for API responses.
func TestClientJSON(t *testing.T) {
	cl := Client{MAC: "aa:bb", IP: "1.2.3.4", RSSI: -70}
	b, err := json.Marshal(cl)
	if err != nil {
		t.Fatal(err)
	}
	var cl2 Client
	if err := json.Unmarshal(b, &cl2); err != nil {
		t.Fatal(err)
	}
	if cl2.RSSI != -70 {
		t.Errorf("RSSI round-trip: got %d", cl2.RSSI)
	}
}
