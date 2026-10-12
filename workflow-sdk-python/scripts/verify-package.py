"""Build once, verify wheel and sdist in external environments, record identity."""
import hashlib
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import venv

sdk = Path(__file__).resolve().parents[1]
dist = sdk / "dist"
artifacts = sdk / "artifacts"
artifacts.mkdir(exist_ok=True)
shutil.rmtree(dist, ignore_errors=True)
subprocess.run([sys.executable, "-m", "build"], cwd=sdk, check=True)
files = sorted(dist.glob("*"))
assert len(files) == 2 and len(list(dist.glob("*.whl"))) == 1 and len(list(dist.glob("*.tar.gz"))) == 1
metadata = {"source": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=sdk, text=True).strip(),
            "dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=sdk)),
            "python": sys.version, "version": re.search(r'(?m)^version = "([^"]+)"', (sdk / "pyproject.toml").read_text()).group(1), "distributions": {}}
for distribution in files:
    with tempfile.TemporaryDirectory(prefix="rochallor-python-consumer-") as directory:
        root = Path(directory)
        venv.create(root / "venv", with_pip=True)
        python = root / "venv" / ("Scripts/python.exe" if os.name == "nt" else "bin/python")
        subprocess.run([str(python), "-m", "pip", "install", str(distribution)], cwd=root, check=True)
        subprocess.run([str(python), "-m", "pip", "check"], cwd=root, check=True)
        consumer = root / "consumer.py"
        shutil.copy2(sdk / "tests/package/consumer.py", consumer)
        subprocess.run([str(python), str(consumer)], cwd=root, check=True)
        installed = Path(subprocess.check_output([str(python), "-c", "import workflow_sdk; print(workflow_sdk.__path__[0])"], cwd=root, text=True).strip())
        # Prove the gate detects a missing packaged runtime module.
        module = installed / "runner/runner.py"
        original = module.read_bytes()
        module.unlink()
        broken = subprocess.run([str(python), str(consumer)], cwd=root, capture_output=True, text=True)
        assert broken.returncode != 0 and "ModuleNotFoundError" in broken.stderr, broken.stderr
        module.write_bytes(original)
        subprocess.run([str(python), str(consumer)], cwd=root, check=True)
        dependencies = subprocess.check_output([str(python), "-m", "pip", "freeze"], cwd=root, text=True)
        metadata["distributions"][distribution.name] = {"sha256": hashlib.sha256(distribution.read_bytes()).hexdigest(), "dependencies": dependencies}
(dist / "SHA256SUMS").write_text("".join(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n" for path in files))
(artifacts / "package.json").write_text(json.dumps(metadata, indent=2) + "\n")
print("Wheel, sdist and negative consumer gates passed")
