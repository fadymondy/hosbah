package apm

import "testing"

func TestFingerprint_GroupsEquivalentExceptions(t *testing.T) {
	stack1 := "at charge (payment.go:42)\nat handle (orders.go:10)\nat requestID=1234567890"
	stack2 := "at charge (payment.go:42)\nat handle (orders.go:10)\nat requestID=9988776655"

	fp1 := Fingerprint("PaymentError", "card declined for order 12345", stack1)
	fp2 := Fingerprint("PaymentError", "card declined for order 98765", stack2)
	if fp1 != fp2 {
		t.Fatalf("expected same fingerprint for exceptions differing only by ids, got %s vs %s", fp1, fp2)
	}
}

func TestFingerprint_DifferentTypeOrMessageDiffers(t *testing.T) {
	stack := "at charge (payment.go:42)"
	fp1 := Fingerprint("PaymentError", "card declined", stack)
	fp2 := Fingerprint("NetworkError", "card declined", stack)
	if fp1 == fp2 {
		t.Fatal("different exception types should not fingerprint the same")
	}

	fp3 := Fingerprint("PaymentError", "card declined", stack)
	fp4 := Fingerprint("PaymentError", "insufficient funds", stack)
	if fp3 == fp4 {
		t.Fatal("different messages should not fingerprint the same")
	}
}

func TestFingerprint_Deterministic(t *testing.T) {
	a := Fingerprint("Err", "msg 1", "frame 1\nframe 2")
	b := Fingerprint("Err", "msg 1", "frame 1\nframe 2")
	if a != b {
		t.Fatal("fingerprint should be deterministic for identical input")
	}
	if len(a) != 64 {
		t.Fatalf("expected sha256 hex digest (64 chars), got %d", len(a))
	}
}

func TestNormalizeMessage(t *testing.T) {
	got := normalizeMessage("user 12345 not found (id=abcdef1234567890)")
	want := "user # not found (id=#)"
	if got != want {
		t.Fatalf("normalizeMessage: got %q want %q", got, want)
	}
}
