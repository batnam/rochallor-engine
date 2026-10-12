"""Build an unsigned Maven candidate, verify it externally, or restore verified bytes for signing."""
import hashlib
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET
import zipfile

sdk = Path(__file__).resolve().parents[1]
artifacts = sdk / "artifacts"
candidate = artifacts / "candidate"
manifest = candidate / "package.json"
gradle = str(sdk / "gradlew")


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def restore():
    metadata = json.loads(manifest.read_text())
    source = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=sdk, text=True).strip()
    assert metadata["source"] == source, "candidate source mismatch"
    tag = os.environ.get("GITHUB_REF_NAME", "")
    if tag.startswith("sdk-java-v"):
        assert metadata["version"] == tag.removeprefix("sdk-java-v"), "candidate version mismatch"
    for name, expected in metadata["files"].items():
        path = candidate / name
        assert sha(path) == expected, f"candidate checksum mismatch: {name}"
    for name in metadata["restore"]:
        target = sdk / name
        assert target.resolve().is_relative_to(sdk / "build")
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(candidate / name, target)
    print("Verified candidate restored for signing/publication:", metadata["version"])


if "--restore" in sys.argv:
    restore()
    sys.exit(0)

artifacts.mkdir(exist_ok=True)
command = [gradle, "clean", "test", "publishMavenPublicationToCandidateRepository", "-PunsignedCandidate", "--no-daemon"]
release = os.environ.get("RELEASE_VERSION", "").removeprefix("sdk-java-v")
if release:
    assert re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", release)
    command.append("-Pversion=" + release)
subprocess.run(command, cwd=sdk, check=True)
repository = sdk / "build/candidate-repository"
poms = list(repository.glob("io/github/batnam/workflow-sdk-java/*/*.pom"))
assert len(poms) == 1, poms
pom = poms[0]
version = ET.parse(pom).findtext("{http://maven.apache.org/POM/4.0.0}version")
jar = pom.with_suffix(".jar")
original = jar.read_bytes()
with tempfile.TemporaryDirectory(prefix="rochallor-java-consumer-") as directory:
    root = Path(directory)
    shutil.copytree(sdk / "test-consumer", root, dirs_exist_ok=True)
    command = [gradle, "clean", "run", "--no-daemon", "--refresh-dependencies", f"-PcandidateRepository={repository.as_uri()}", f"-PsdkVersion={version}"]
    subprocess.run(command, cwd=root, check=True)
    # Missing public Runner must fail compile/runtime in the external project.
    broken = root / "broken.jar"
    with zipfile.ZipFile(jar) as src, zipfile.ZipFile(broken, "w") as dst:
        for item in src.infolist():
            if not item.filename.endswith("/runner/Runner.class"):
                dst.writestr(item, src.read(item.filename))
    shutil.copy2(broken, jar)
    try:
        result = subprocess.run(command, cwd=root, capture_output=True, text=True)
        assert result.returncode != 0 and "Runner" in result.stdout + result.stderr, "broken artifact gate did not fail for missing Runner"
        print("Broken Maven artifact rejected (missing Runner)")
    finally:
        jar.write_bytes(original)
    subprocess.run(command, cwd=root, check=True)

shutil.rmtree(candidate, ignore_errors=True)
candidate.mkdir(parents=True)
shutil.copytree(repository, candidate / "repository")
(candidate / "version.txt").write_text(version + "\n")
restore_files = []
for source in [*list((sdk / "build/libs").glob("*.jar")), sdk / "build/publications/maven/pom-default.xml", sdk / "build/publications/maven/module.json"]:
    assert source.is_file(), source
    name = source.relative_to(sdk).as_posix()
    target = candidate / name
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, target)
    restore_files.append(name)
metadata = {"source": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=sdk, text=True).strip(),
            "dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=sdk)),
            "version": version, "restore": restore_files,
            "files": {path.relative_to(candidate).as_posix(): sha(path) for path in sorted(candidate.rglob("*")) if path.is_file()}}
manifest.write_text(json.dumps(metadata, indent=2) + "\n")
(candidate / "SHA256SUMS").write_text("".join(f"{digest}  {name}\n" for name, digest in metadata["files"].items()))
with (artifacts / "dependencies.txt").open("w") as log:
    subprocess.run([gradle, "dependencies", "--configuration", "runtimeClasspath", "--no-daemon"], cwd=sdk, stdout=log, stderr=subprocess.STDOUT, check=True)
restore()
print("Maven artifact and negative consumer gates passed:", version)
