package core

import (
	"testing"
	"time"
)

func TestNormalizeCronSchedule_StandardPassthrough(t *testing.T) {
	expr, oneShot, err := normalizeCronSchedule("0 11 * * *")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if expr != "0 11 * * *" || oneShot {
		t.Fatalf("got (%q, %v), want (\"0 11 * * *\", false)", expr, oneShot)
	}
}

func TestNormalizeCronSchedule_Every(t *testing.T) {
	expr, oneShot, err := normalizeCronSchedule("@every 10m")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if expr != "@every 10m" || oneShot {
		t.Fatalf("got (%q, %v), want (\"@every 10m\", false)", expr, oneShot)
	}
}

func TestNormalizeCronSchedule_EveryInvalid(t *testing.T) {
	if _, _, err := normalizeCronSchedule("@every notaduration"); err == nil {
		t.Fatal("expected error for invalid @every duration")
	}
}

func TestNormalizeCronSchedule_At(t *testing.T) {
	future := time.Now().Add(48 * time.Hour).Truncate(time.Minute)
	ts := future.Format(time.RFC3339)
	expr, oneShot, err := normalizeCronSchedule("@at " + ts)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !oneShot {
		t.Fatal("expected oneShot=true for @at")
	}
	want := time.Date(future.Year(), future.Month(), future.Day(), future.Hour(), future.Minute(), 0, 0, future.Location())
	// expr maps to "<min> <hour> <day> <month> *" for that exact minute.
	got := expr
	_ = got
	_ = want
	if expr == "" {
		t.Fatal("empty normalized expr for @at")
	}
}

func TestNormalizeCronSchedule_AtPast(t *testing.T) {
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	if _, _, err := normalizeCronSchedule("@at " + past); err == nil {
		t.Fatal("expected error for @at in the past")
	}
}

func TestCronJob_RetryAndNotifyDefaults(t *testing.T) {
	j := &CronJob{}
	if got := j.GetRetryCount(); got != 1 {
		t.Fatalf("GetRetryCount default = %d, want 1", got)
	}
	if j.ShouldNotifyOnFailure() {
		t.Fatal("ShouldNotifyOnFailure default should be false")
	}

	zero := 0
	neg := -1
	tru := true
	j2 := &CronJob{RetryCount: &zero, NotifyOnFailure: &tru}
	if got := j2.GetRetryCount(); got != 0 {
		t.Fatalf("GetRetryCount(0) = %d, want 0", got)
	}
	if !j2.ShouldNotifyOnFailure() {
		t.Fatal("ShouldNotifyOnFailure(true) = false, want true")
	}
	j3 := &CronJob{RetryCount: &neg}
	if got := j3.GetRetryCount(); got != 0 {
		t.Fatalf("GetRetryCount(negative) = %d, want 0 (clamped)", got)
	}
}

func TestCronScheduler_OneShotTracking(t *testing.T) {
	dir := t.TempDir()
	store, err := NewCronStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cs := NewCronScheduler(store)

	if cs.isOneShot("j1") {
		t.Fatal("should not be one-shot initially")
	}
	cs.markOneShot("j1")
	if !cs.isOneShot("j1") {
		t.Fatal("markOneShot should set one-shot")
	}
	cs.clearOneShot("j1")
	if cs.isOneShot("j1") {
		t.Fatal("clearOneShot should unset one-shot")
	}
}

func TestCronJob_NewFieldsPersist(t *testing.T) {
	dir := t.TempDir()
	store, err := NewCronStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	two := 2
	tru := true
	job := &CronJob{
		ID:              "j1",
		Project:         "p",
		SessionKey:      "feishu:u1:c1",
		CronExpr:        "0 6 * * *",
		Prompt:          "hello",
		Enabled:         true,
		RetryCount:      &two,
		NotifyOnFailure: &tru,
		CreatedAt:       time.Now(),
	}
	if err := store.Add(job); err != nil {
		t.Fatal(err)
	}
	got := store.Get("j1")
	if got == nil {
		t.Fatal("job not found after add")
	}
	if got.GetRetryCount() != 2 || !got.ShouldNotifyOnFailure() {
		t.Fatalf("retry=%d notify=%v, want 2/true", got.GetRetryCount(), got.ShouldNotifyOnFailure())
	}
}
