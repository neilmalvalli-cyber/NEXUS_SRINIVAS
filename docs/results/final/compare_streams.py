"""Compare two nexus-torture runs made with the same seed.

For every iteration: the iteration seed and planned kill time must be equal,
and for every writer the sequence of requests (id, kind, effects) of one run
must be a prefix of the other's (timing decides how far each got before the
kill, not what was sent).
"""
import json, sys
from collections import defaultdict


def load(path):
    starts, sends = {}, defaultdict(list)
    with open(path, encoding="utf-8") as f:
        for line in f:
            e = json.loads(line)
            if e.get("ev") == "start":
                starts[e["iter"]] = (e.get("iter_seed"), e.get("kill_after_ms"))
            elif e.get("ev") == "send":
                key = (e["iter"], e.get("writer"))
                sends[key].append((e["op"], e.get("kind"), json.dumps(e.get("effects"), sort_keys=True), e.get("key")))
    return starts, sends


a_starts, a_sends = load(sys.argv[1])
b_starts, b_sends = load(sys.argv[2])
problems = 0
if a_starts != b_starts:
    problems += 1
    print("iteration seeds / kill times differ")
compared = identical = 0
for key in sorted(set(a_sends) | set(b_sends), key=str):
    x, y = a_sends.get(key, []), b_sends.get(key, [])
    n = min(len(x), len(y))
    compared += n
    if x[:n] != y[:n]:
        problems += 1
        i = next(i for i in range(n) if x[i] != y[i])
        print("stream differs at", key, "index", i, x[i], y[i])
    if x == y:
        identical += 1
print(f"iterations={len(a_starts)} seeds-and-kill-times-equal={a_starts == b_starts} "
      f"writer-streams={len(set(a_sends) | set(b_sends))} fully-identical={identical} "
      f"requests-compared={compared} mismatches={problems}")
sys.exit(1 if problems else 0)
