package index

import "testing"

func TestIndexPutGetDeleteAndCounters(t *testing.T) {
	x := New(4)
	if _, ok := x.Get([]byte("a")); ok || x.Len() != 0 || x.LiveBytes() != 0 {
		t.Fatal("new index not empty")
	}
	x.Put("a", Loc{Off: 32, Size: 20})
	x.Put("b", Loc{Off: 52, Size: 30})
	if l, ok := x.Get([]byte("a")); !ok || l != (Loc{32, 20}) {
		t.Fatalf("Get(a) = %v %v", l, ok)
	}
	if x.Len() != 2 || x.LiveBytes() != 50 {
		t.Fatalf("Len=%d Live=%d", x.Len(), x.LiveBytes())
	}
	// Overwrite: the old entry stops counting as live.
	x.Put("a", Loc{Off: 100, Size: 15})
	if l, _ := x.Get([]byte("a")); l.Off != 100 || x.Len() != 2 || x.LiveBytes() != 45 {
		t.Fatalf("after overwrite: %v Len=%d Live=%d", l, x.Len(), x.LiveBytes())
	}
	if !x.Delete([]byte("b")) || x.Delete([]byte("b")) || x.Delete([]byte("zz")) {
		t.Fatal("Delete results wrong")
	}
	if x.Len() != 1 || x.LiveBytes() != 15 {
		t.Fatalf("after delete: Len=%d Live=%d", x.Len(), x.LiveBytes())
	}
	n := 0
	x.Range(func(k string, l Loc) bool { n++; return true })
	if n != 1 {
		t.Fatalf("Range visited %d", n)
	}
	if New(-1) == nil {
		t.Fatal("New(-1) returned nil")
	}
}

func TestIndexGetDoesNotAllocate(t *testing.T) {
	x := New(1)
	x.Put("key", Loc{Off: 32, Size: 14})
	k := []byte("key")
	if a := testing.AllocsPerRun(100, func() { x.Get(k) }); a != 0 {
		t.Fatalf("Get allocates %v times", a)
	}
}
