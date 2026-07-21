package accesslog

import (
	"testing"
	"time"
)

func TestAppendAndRecent(t *testing.T) {
	l := New(5)
	l.Append(Entry{SrcIP: "1.1.1.1", Verdict: VerdictAllow})
	l.Append(Entry{SrcIP: "2.2.2.2", Verdict: VerdictDeny, Reason: "境外"})

	all := l.Recent(Filter{}, 0)
	if len(all) != 2 {
		t.Fatalf("len=%d want 2", len(all))
	}
	// 倒序:最新在前
	if all[0].SrcIP != "2.2.2.2" {
		t.Errorf("all[0].SrcIP=%q want 2.2.2.2", all[0].SrcIP)
	}
	if all[1].SrcIP != "1.1.1.1" {
		t.Errorf("all[1].SrcIP=%q want 1.1.1.1", all[1].SrcIP)
	}
}

func TestRingOverwrite(t *testing.T) {
	l := New(3)
	for i := 0; i < 5; i++ {
		l.Append(Entry{SrcIP: string(rune('0' + i))}) // "0".."4"
	}
	// cap=3,只保留最后 3 个:2,3,4(倒序 4,3,2)
	all := l.Recent(Filter{}, 0)
	if len(all) != 3 {
		t.Fatalf("len=%d want 3", len(all))
	}
	want := []string{"4", "3", "2"}
	for i, w := range want {
		if all[i].SrcIP != w {
			t.Errorf("all[%d].SrcIP=%q want %q", i, all[i].SrcIP, w)
		}
	}
	if l.Count() != 3 {
		t.Errorf("Count=%d want 3", l.Count())
	}
}

func TestFilter(t *testing.T) {
	l := New(10)
	l.Append(Entry{SrcIP: "1.1.1.1", Verdict: VerdictAllow})
	l.Append(Entry{SrcIP: "2.2.2.2", Verdict: VerdictDeny})
	l.Append(Entry{SrcIP: "1.1.1.1", Verdict: VerdictDeny})

	if n := len(l.Recent(Filter{Verdict: VerdictDeny}, 0)); n != 2 {
		t.Errorf("denied len=%d want 2", n)
	}
	if n := len(l.Recent(Filter{SrcIP: "1.1.1.1"}, 0)); n != 2 {
		t.Errorf("byIP len=%d want 2", n)
	}
	if n := len(l.Recent(Filter{SrcIP: "1.1.1.1", Verdict: VerdictDeny}, 0)); n != 1 {
		t.Errorf("both len=%d want 1", n)
	}
}

func TestMaxLimit(t *testing.T) {
	l := New(100)
	for i := 0; i < 50; i++ {
		l.Append(Entry{SrcIP: "x"})
	}
	if n := len(l.Recent(Filter{}, 10)); n != 10 {
		t.Fatalf("len=%d want 10", n)
	}
}

func TestTimeAutoFill(t *testing.T) {
	l := New(5)
	l.Append(Entry{SrcIP: "1.1.1.1"})
	all := l.Recent(Filter{}, 0)
	if all[0].Time.IsZero() {
		t.Fatal("Time 未自动填充")
	}
	if time.Since(all[0].Time) > 5*time.Second {
		t.Error("Time 不正确")
	}
}

func TestNilSafety(t *testing.T) {
	var l *Logger
	l.Append(Entry{}) // 不应 panic
	if l.Recent(Filter{}, 0) != nil {
		t.Error("nil Recent 应返回 nil")
	}
	if l.Count() != 0 {
		t.Error("nil Count 应返回 0")
	}
}

func TestDefaultCapacity(t *testing.T) {
	l := New(0)
	l.Append(Entry{})
	if l.Count() != 1 {
		t.Errorf("默认容量写入失败: Count=%d", l.Count())
	}
}
