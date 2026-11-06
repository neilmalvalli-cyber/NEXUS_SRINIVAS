# kvdb-bench results

Started 2026-11-06T23:09:00+05:30, 3 run(s), 229 s in total.

| Machine | |
|---|---|
| cpu | AMD Ryzen 7 5800H with Radeon Graphics |
| cpus | 16 |
| go | go1.27.3 |
| host | DESKTOP-DSGLTLJ |
| os | windows/amd64 |
| os_version | Windows 11 Home Single Language build 26200 (25H2) |

| Configuration | |
|---|---|
| api | in-process src/kvdb |
| batch_size | 1000 |
| durability | every put/delete/batch synced before it returns |
| n | 100000 |
| runs | 3 |
| seed | 1 |
| value_size | 100 |

Note: NVMe SSD, Windows Defender real-time protection on

Median of the runs (by phase duration):

| Phase | Ops | Seconds | Ops/s | p50 µs | p99 µs | Automatic compactions | File before | File after |
|---|---|---|---|---|---|---|---|---|
| put | 100000 | 37.053 | 2699 | 520 | 1064 | 0 | — | 15500032 |
| reopen (recovery) | 100000 | 0.045 | 2206590 | — | — | 0 | — | 15500032 |
| get | 100000 | 0.604 | 165652 | — | — | 0 | — | 15500032 |
| batch | 100000 | 0.553 | 180946 | 1034 | 460836 | 1 | — | 15498960 |
| compact (n live keys) | 100000 | 0.459 | — | — | — | 0 | 15498960 | 12300128 |
| delete | 100000 | 37.488 | 2668 | 520 | 1266 | 3 | — | 3680926 |
| compact (empty) | 0 | 0.006 | — | — | — | 0 | 3680926 | 32 |

Every run:

| Run | Phase | Seconds | Ops/s |
|---|---|---|---|
| 1 | put | 36.941 | 2707 |
| 1 | reopen (recovery) | 0.046 | 2194002 |
| 1 | get | 0.608 | 164518 |
| 1 | batch | 0.553 | 180946 |
| 1 | compact (n live keys) | 0.455 | — |
| 1 | delete | 37.636 | 2657 |
| 1 | compact (empty) | 0.018 | — |
| 2 | put | 37.264 | 2684 |
| 2 | reopen (recovery) | 0.045 | 2246076 |
| 2 | get | 0.599 | 166985 |
| 2 | batch | 0.562 | 177790 |
| 2 | compact (n live keys) | 0.459 | — |
| 2 | delete | 37.488 | 2668 |
| 2 | compact (empty) | 0.006 | — |
| 3 | put | 37.053 | 2699 |
| 3 | reopen (recovery) | 0.045 | 2206590 |
| 3 | get | 0.604 | 165652 |
| 3 | batch | 0.539 | 185650 |
| 3 | compact (n live keys) | 0.459 | — |
| 3 | delete | 37.372 | 2676 |
| 3 | compact (empty) | 0.006 | — |

"batch" ops/s counts operations (n), not batches. "reopen" ops/s is keys recovered per second. Every write is synced before it returns.
