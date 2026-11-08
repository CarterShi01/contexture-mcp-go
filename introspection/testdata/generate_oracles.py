"""Validation-only stdlib oracles; never imported by the native Go runtime."""
import hashlib
import json
import math
import pathlib
import struct
import sys
import unicodedata

assert unicodedata.unidata_version == "15.0.0", "qualify Unicode 15 before regenerating"
output = pathlib.Path("introspection") / "testdata"
state = 0x123456789ABCDEF0
rows = []
while len(rows) < 10000:
    state ^= (state << 13) & ((1 << 64) - 1)
    state ^= state >> 7
    state ^= (state << 17) & ((1 << 64) - 1)
    state &= (1 << 64) - 1
    value = struct.unpack(">d", state.to_bytes(8, "big"))[0]
    if math.isfinite(value):
        canonical = json.dumps(
            {"value": int(value) if value.is_integer() else value},
            ensure_ascii=False, separators=(",", ":"), sort_keys=True,
        )
        rows.append({"Bits": f"{state:016x}", "Canonical": canonical})
folds = [
    {"Text": chr(n), "Fold": chr(n).casefold()}
    for n in range(0x110000)
    if not 0xD800 <= n <= 0xDFFF and chr(n).casefold() != chr(n)
]
assert len(folds) == 1530
hashes = {}
for name, data in [("binary64-oracle.json", rows), ("casefold-oracle.json", folds)]:
    raw = (json.dumps(data, ensure_ascii=False, separators=(",", ":")) + "\n").encode()
    (output / name).write_bytes(raw)
    hashes[name] = hashlib.sha256(raw).hexdigest()
    print(name, len(data), hashes[name])
provenance = {
    "producer": "validation-only stdlib; Go independently formats binary64 bits/cases.Fold",
    "python": sys.version, "unicode": unicodedata.unidata_version,
    "seed": "123456789abcdef0", "algorithm": "xorshift64 13/7/17; finite values only",
    "binary64_count": len(rows), "casefold_count": len(folds), "sha256": hashes,
}
path = pathlib.Path("docs/verification/go-static-catalog-debug-20261107/oracle-provenance.json")
path.write_text(json.dumps(provenance, indent=2) + "\n", encoding="utf-8")
