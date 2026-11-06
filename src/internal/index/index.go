package index

// Loc locates one PUT entry in the log.
type Loc struct {
	// Off is the absolute file offset of the entry header.
	Off int64
	// Size is the entry's full encoded size (11-byte header + key + value).
	// It is at most format.MaxEntrySize, so it fits in uint32.
	Size uint32
}

// Index maps keys to the location of their latest PUT entry.
type Index struct {
	m         map[string]Loc
	liveBytes uint64 // sum of Size over all entries in m
}

// New returns an empty index with room for about hint keys.
func New(hint int) *Index {
	if hint < 0 {
		hint = 0
	}
	return &Index{m: make(map[string]Loc, hint)}
}

// Get returns the location of key. It does not allocate.
func (x *Index) Get(key []byte) (Loc, bool) {
	l, ok := x.m[string(key)]
	return l, ok
}

// Put records loc as the latest PUT of key, replacing any previous entry
// (which becomes dead log data).
func (x *Index) Put(key string, loc Loc) {
	if old, ok := x.m[key]; ok {
		x.liveBytes -= uint64(old.Size)
	}
	x.m[key] = loc
	x.liveBytes += uint64(loc.Size)
}

// Delete removes key, reporting whether it was present.
func (x *Index) Delete(key []byte) bool {
	old, ok := x.m[string(key)]
	if !ok {
		return false
	}
	delete(x.m, string(key))
	x.liveBytes -= uint64(old.Size)
	return true
}

// Len returns the number of live keys.
func (x *Index) Len() int { return len(x.m) }

// LiveBytes returns the total encoded size of the live PUT entries. All
// other log bytes after the superblock (frame headers, DEL entries,
// overwritten PUTs) are dead.
func (x *Index) LiveBytes() uint64 { return x.liveBytes }

// Range calls fn for every key in unspecified order until fn returns false.
// The key must not be retained beyond the call if the index may change.
func (x *Index) Range(fn func(key string, loc Loc) bool) {
	for k, l := range x.m {
		if !fn(k, l) {
			return
		}
	}
}
