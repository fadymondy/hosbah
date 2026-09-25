package agentproto

import "testing"

const (
	testDrainID = "00000000-0000-4000-8000-0000000000d1"
	testTeamID  = "00000000-0000-4000-8000-0000000000e1"
	testAppID   = "00000000-0000-4000-8000-0000000000a1"
)

func TestSyslogSinkValidate(t *testing.T) {
	cases := []struct {
		name    string
		sink    SyslogSink
		wantErr bool
	}{
		{"tcp+tls ok", SyslogSink{Transport: SyslogTransportTCPTLS, Host: "syslog.example.test", Port: 6514}, false},
		{"udp ok", SyslogSink{Transport: SyslogTransportUDP, Host: "10.0.0.5", Port: 514}, false},
		{"bad transport", SyslogSink{Transport: "quic", Host: "syslog.example.test", Port: 514}, true},
		{"bad host", SyslogSink{Transport: SyslogTransportUDP, Host: "not a host!", Port: 514}, true},
		{"bad port low", SyslogSink{Transport: SyslogTransportUDP, Host: "syslog.example.test", Port: 0}, true},
		{"bad port high", SyslogSink{Transport: SyslogTransportUDP, Host: "syslog.example.test", Port: 70000}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.sink.Validate()
			if (err != nil) != c.wantErr {
				t.Errorf("Validate() = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestHTTPSinkValidate(t *testing.T) {
	cases := []struct {
		name     string
		sink     HTTPSink
		internal bool
		wantErr  bool
	}{
		{"https ok", HTTPSink{URL: "https://collector.example.test/ingest"}, false, false},
		{"http refused when not internal", HTTPSink{URL: "http://collector.example.test/ingest"}, false, true},
		{"http ok when internal", HTTPSink{URL: "http://10.0.0.5:8080/ingest"}, true, false},
		{"bad scheme", HTTPSink{URL: "ftp://collector.example.test/ingest"}, false, true},
		{"bad url", HTTPSink{URL: "not a url"}, false, true},
		{"bad auth header name", HTTPSink{URL: "https://collector.example.test/ingest", AuthHeader: "bad header!"}, false, true},
		{"good auth header name", HTTPSink{URL: "https://collector.example.test/ingest", AuthHeader: "Authorization"}, false, false},
		{"bad port", HTTPSink{URL: "https://collector.example.test:99999/ingest"}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.sink.Validate(c.internal)
			if (err != nil) != c.wantErr {
				t.Errorf("Validate(%v) = %v, wantErr %v", c.internal, err, c.wantErr)
			}
		})
	}
}

func TestLokiSinkValidate(t *testing.T) {
	cases := []struct {
		name     string
		sink     LokiSink
		internal bool
		wantErr  bool
	}{
		{"ok, no labels", LokiSink{URL: "https://loki.example.test/loki/api/v1/push"}, false, false},
		{"ok with labels", LokiSink{URL: "https://loki.example.test/loki/api/v1/push", Labels: map[string]string{"env": "prod"}}, false, false},
		{"http refused when not internal", LokiSink{URL: "http://loki.example.test/loki/api/v1/push"}, false, true},
		{"bad label key", LokiSink{URL: "https://loki.example.test/loki/api/v1/push", Labels: map[string]string{"1bad": "x"}}, false, true},
		{"label value too long", LokiSink{URL: "https://loki.example.test/loki/api/v1/push", Labels: map[string]string{"k": string(make([]byte, MaxDrainLabelLen+1))}}, false, true},
		{"too many labels", func() LokiSink {
			labels := map[string]string{}
			for i := 0; i < MaxDrainLabels+1; i++ {
				labels[randLabelKey(i)] = "v"
			}
			return LokiSink{URL: "https://loki.example.test/loki/api/v1/push", Labels: labels}
		}(), false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.sink.Validate(c.internal)
			if (err != nil) != c.wantErr {
				t.Errorf("Validate(%v) = %v, wantErr %v", c.internal, err, c.wantErr)
			}
		})
	}
}

func randLabelKey(i int) string {
	// distinct, valid loki label keys k0, k1, k2, ...
	digits := "0123456789"
	out := []byte{'k'}
	if i == 0 {
		return "k0"
	}
	for i > 0 {
		out = append(out, digits[i%10])
		i /= 10
	}
	return string(out)
}

func TestDrainConfigValidate(t *testing.T) {
	syslog := &SyslogSink{Transport: SyslogTransportUDP, Host: "syslog.example.test", Port: 514}
	base := DrainConfig{DrainID: testDrainID, TeamID: testTeamID, Kind: DrainKindSyslog, Syslog: syslog}

	if err := base.Validate(); err != nil {
		t.Fatalf("a valid syslog drain should validate: %v", err)
	}

	scoped := base
	scoped.AppID = testAppID
	if err := scoped.Validate(); err != nil {
		t.Errorf("a valid app-scoped drain should validate: %v", err)
	}

	badApp := base
	badApp.AppID = "not-a-uuid"
	if err := badApp.Validate(); err == nil {
		t.Error("a bad app id should not validate")
	}

	badIDs := DrainConfig{DrainID: "nope", TeamID: "nope", Kind: DrainKindSyslog, Syslog: syslog}
	if err := badIDs.Validate(); err == nil {
		t.Error("bad drain/team ids should not validate")
	}

	// A remove is valid with only ids set — no sink details required.
	remove := DrainConfig{DrainID: testDrainID, TeamID: testTeamID, Remove: true}
	if err := remove.Validate(); err != nil {
		t.Errorf("a remove should validate without sink settings: %v", err)
	}

	noKind := DrainConfig{DrainID: testDrainID, TeamID: testTeamID}
	if err := noKind.Validate(); err == nil {
		t.Error("an unknown kind should not validate")
	}

	missingSyslog := DrainConfig{DrainID: testDrainID, TeamID: testTeamID, Kind: DrainKindSyslog}
	if err := missingSyslog.Validate(); err == nil {
		t.Error("a syslog drain without syslog settings should not validate")
	}

	http := DrainConfig{DrainID: testDrainID, TeamID: testTeamID, Kind: DrainKindHTTP,
		HTTP: &HTTPSink{URL: "https://collector.example.test/ingest"}}
	if err := http.Validate(); err != nil {
		t.Errorf("a valid http drain should validate: %v", err)
	}
	missingHTTP := DrainConfig{DrainID: testDrainID, TeamID: testTeamID, Kind: DrainKindHTTP}
	if err := missingHTTP.Validate(); err == nil {
		t.Error("an http drain without http settings should not validate")
	}

	loki := DrainConfig{DrainID: testDrainID, TeamID: testTeamID, Kind: DrainKindLoki,
		Loki: &LokiSink{URL: "https://loki.example.test/loki/api/v1/push"}}
	if err := loki.Validate(); err != nil {
		t.Errorf("a valid loki drain should validate: %v", err)
	}
	missingLoki := DrainConfig{DrainID: testDrainID, TeamID: testTeamID, Kind: DrainKindLoki}
	if err := missingLoki.Validate(); err == nil {
		t.Error("a loki drain without loki settings should not validate")
	}
}

func TestDrainStatusParamsValidate(t *testing.T) {
	if err := (DrainStatusParams{DrainID: testDrainID}).Validate(); err != nil {
		t.Errorf("a valid drain id should validate: %v", err)
	}
	if err := (DrainStatusParams{DrainID: "nope"}).Validate(); err == nil {
		t.Error("a bad drain id should not validate")
	}
}

func TestDrainTestParamsValidate(t *testing.T) {
	valid := DrainTestParams{Drain: DrainConfig{DrainID: testDrainID, TeamID: testTeamID, Kind: DrainKindSyslog,
		Syslog: &SyslogSink{Transport: SyslogTransportUDP, Host: "syslog.example.test", Port: 514}}}
	if err := valid.Validate(); err != nil {
		t.Errorf("a valid test params should validate: %v", err)
	}
	invalid := DrainTestParams{Drain: DrainConfig{DrainID: testDrainID, TeamID: testTeamID, Kind: "carrier-pigeon"}}
	if err := invalid.Validate(); err == nil {
		t.Error("an invalid drain config should not validate")
	}
}
