package clock

import (
	"testing"
	"time"
)

func TestNowIsUTCAndMicrosecondPrecision(t *testing.T) {
	now := System{}.Now()
	if now.Location() != time.UTC {
		t.Errorf("fuso %v, esperado UTC", now.Location())
	}
	if now.Nanosecond()%1000 != 0 {
		t.Errorf("precisão maior que microssegundos: %d ns", now.Nanosecond())
	}
	if time.Since(now) > time.Minute || now.After(time.Now().Add(time.Second)) {
		t.Errorf("horário fora do esperado: %v", now)
	}
}
