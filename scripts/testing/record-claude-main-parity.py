"""Record the Claude response fixture oracle from the pinned official checkout.

Usage: python scripts/testing/record-claude-main-parity.py /path/to/official-main
The checkout is read only; Go's overlay supplies the fixture test temporarily.
"""

import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile

BASELINE = "bd6bb53019fb7e21804b31ab7365c9b2097f4b6c"
root = Path(__file__).resolve().parents[2]
checkout = Path(sys.argv[1]).resolve()
revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=checkout, text=True).strip()
if revision != BASELINE:
    raise SystemExit(f"Expected official baseline {BASELINE}, got {revision}")
source = (root / "relay/channel/claude/stream_policy_test.go").read_text(encoding="utf-8")
source = source.replace('\t"github.com/QuantumNous/new-api/service"\n', "")
source = re.sub(r"\s*// BEGIN fork setup:.*?// END fork setup\.", "", source, flags=re.S)
source = re.sub(r"^.*// fork finalizer\n", "", source, flags=re.M)
source = re.sub(r"\tgolden, err := os.ReadFile.*?\tactual :=", "\tactual :=", source, flags=re.S)
source = re.sub(r"\s*want, err := common.Marshal\(expected\[key\]\).*?require.JSONEq\(t, string\(want\), string\(got\), downstream\)", "", source, flags=re.S)
with tempfile.TemporaryDirectory(prefix="claude-main-parity-") as temp:
    temp = Path(temp)
    output = temp / "parity.json"
    source = source.replace('require.Len(t, expected, len(actual), "golden must cover exactly this fixture matrix")',
        'encoded, err := common.Marshal(actual)\nrequire.NoError(t, err)\nrequire.NoError(t, os.WriteFile(' + json.dumps(output.as_posix()) + ', encoded, 0600))')
    test_file = temp / "parity_test.go"
    test_file.write_text(source, encoding="utf-8")
    overlay = temp / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {
        str(checkout / "relay/channel/claude/zz_record_parity_test.go"): str(test_file)
    }}), encoding="utf-8")
    subprocess.run(["go", "test", f"-overlay={overlay}", "./relay/channel/claude", "-run", "^TestClaudeMainStreamParity$", "-count=1"], cwd=checkout, check=True)
    result = json.loads(output.read_text(encoding="utf-8"))
    target = root / "relay/channel/claude/testdata/main-stream-parity.json"
    target.write_text(json.dumps(result, indent=2, ensure_ascii=False, sort_keys=True) + "\n", encoding="utf-8")
    print(f"Recorded {len(result)} fixtures from {BASELINE}")
