package monitor

import (
	"net/url"
	"testing"

	mrand "math/rand"
)

func snap(id, name string, available bool, latencyMs int64, rate float64) Snapshot {
	return Snapshot{
		NodeInfo:         NodeInfo{StableID: id, Name: name, Mode: "http"},
		Available:        available,
		InitialCheckDone: true,
		LastLatencyMs:    latencyMs,
		AvailabilityRate: rate,
		TotalAttempts:    10,
	}
}

func TestPickCallerID(t *testing.T) {
	cases := []struct {
		in   *authInfo
		want string
	}{
		{nil, "anon"},
		{&authInfo{Method: ""}, "anon"},
		{&authInfo{Method: "session"}, "session"},
		{&authInfo{Method: "none"}, "none"},
		{&authInfo{Method: "apikey", APIKeyID: 7}, "apikey:7"},
	}
	for _, c := range cases {
		if got := pickCallerID(c.in); got != c.want {
			t.Errorf("pickCallerID() = %q, want %q", got, c.want)
		}
	}
}

func TestFilterForPick(t *testing.T) {
	nodes := []Snapshot{
		snap("a", "US-01", true, 100, 0.9),
		snap("b", "JP-01", true, 200, 0.8),
		snap("c", "US-02", false, 300, 0.3), // 不可用，默认应被排除
	}
	nodes[0].CountryCode = "US"
	nodes[1].CountryCode = "JP"
	nodes[2].CountryCode = "US"

	if n := len(filterForPick(nodes, url.Values{})); n != 2 {
		t.Fatalf("default available=true: got %d, want 2", n)
	}
	q := url.Values{"available": []string{"false"}}
	if n := len(filterForPick(nodes, q)); n != 3 {
		t.Fatalf("available=false: got %d, want 3", n)
	}
	q = url.Values{"country": []string{"US,JP"}}
	if n := len(filterForPick(nodes, q)); n != 2 {
		t.Fatalf("country=US,JP: got %d, want 2", n)
	}
	q = url.Values{"country": []string{"US"}}
	if n := len(filterForPick(nodes, q)); n != 1 {
		t.Fatalf("country=US (c excluded by available): got %d, want 1", n)
	}
	q = url.Values{"name_regex": []string{"^JP"}}
	if n := len(filterForPick(nodes, q)); n != 1 {
		t.Fatalf("name_regex=^JP: got %d, want 1", n)
	}
	q = url.Values{"exit_ip": []string{"9.9.9.9"}}
	if n := len(filterForPick(nodes, q)); n != 0 {
		t.Fatalf("exit_ip=9.9.9.9 (no match): got %d, want 0", n)
	}
}

func TestPickNodeRoundRobin(t *testing.T) {
	s := &Server{pickCursors: map[string]int{}, pickRng: mrand.New(mrand.NewSource(1))}
	// 故意乱序，验证内部按 stable_id 稳定排序后再轮询
	cands := []Snapshot{
		snap("c", "n3", true, 100, 0.9),
		snap("a", "n1", true, 100, 0.9),
		snap("b", "n2", true, 100, 0.9),
	}
	key := "caller|rule"
	var seq []string
	for i := 0; i < 4; i++ {
		seq = append(seq, s.pickNode(cands, "round_robin", key).StableID)
	}
	want := []string{"a", "b", "c", "a"}
	for i, w := range want {
		if seq[i] != w {
			t.Errorf("round_robin call %d: got %s, want %s (seq %v)", i, seq[i], w, seq)
		}
	}
}

func TestPickNodeRoundRobinDistinctCallers(t *testing.T) {
	s := &Server{pickCursors: map[string]int{}, pickRng: mrand.New(mrand.NewSource(1))}
	cands := []Snapshot{
		snap("a", "n1", true, 100, 0.9),
		snap("b", "n2", true, 100, 0.9),
	}
	// 不同调用方各自独立游标
	first := s.pickNode(cands, "round_robin", "caller1|rule").StableID
	second := s.pickNode(cands, "round_robin", "caller2|rule").StableID
	if first != "a" || second != "a" {
		t.Errorf("distinct callers should each start at a; got %s,%s", first, second)
	}
}

func TestPickNodeAvailabilityFirst(t *testing.T) {
	s := &Server{pickCursors: map[string]int{}, pickRng: mrand.New(mrand.NewSource(1))}
	cands := []Snapshot{
		snap("a", "lo", true, 100, 0.5),
		snap("b", "hi", true, 100, 0.99),
		snap("c", "mid", true, 100, 0.8),
	}
	if got := s.pickNode(cands, "availability_first", "").StableID; got != "b" {
		t.Errorf("availability_first: got %s, want b (highest rate)", got)
	}
}

func TestPickNodeWeighted(t *testing.T) {
	s := &Server{pickCursors: map[string]int{}, pickRng: mrand.New(mrand.NewSource(1))}
	cands := []Snapshot{
		snap("a", "n1", true, 100, 0.9),
		snap("b", "n2", true, 200, 0.8),
	}
	for i := 0; i < 20; i++ {
		got := s.pickNode(cands, "weighted", "").StableID
		if got != "a" && got != "b" {
			t.Errorf("weighted: got %s, want a or b", got)
		}
	}
}
