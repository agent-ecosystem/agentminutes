#!/usr/bin/env python3
"""Repackage goreleaser release archives into per-platform wheels.

    python3 build_wheels.py --version 0.1.1 --assets <dir> --out <dir>

For each platform: copy this package to a temp dir, stamp the version into
pyproject.toml, drop the extracted binary into src/agentminutes/bin/, build
a wheel, and retag it from py3-none-any to the platform tag. Requires the
``build`` and ``wheel`` packages. The linux tags claim manylinux and
musllinux together, which is safe because the binaries are CGO_ENABLED=0
static; the platform list moves together with .goreleaser.yaml's matrix.
"""

import argparse
import pathlib
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile

PLATFORMS = [
    ("darwin_arm64", "tar.gz", "macosx_11_0_arm64"),
    ("darwin_amd64", "tar.gz", "macosx_10_13_x86_64"),
    ("linux_arm64", "tar.gz", "manylinux_2_17_aarch64.manylinux2014_aarch64.musllinux_1_1_aarch64"),
    ("linux_amd64", "tar.gz", "manylinux_2_17_x86_64.manylinux2014_x86_64.musllinux_1_1_x86_64"),
    ("windows_arm64", "zip", "win_arm64"),
    ("windows_amd64", "zip", "win_amd64"),
]

PACKAGE_FILES = ["pyproject.toml", "README.md", "src"]


def extract_binary(archive: pathlib.Path, exe: str, dest: pathlib.Path) -> None:
    dest.parent.mkdir(parents=True, exist_ok=True)
    if archive.suffix == ".zip":
        with zipfile.ZipFile(archive) as zf, zf.open(exe) as src, open(dest, "wb") as out:
            shutil.copyfileobj(src, out)
    else:
        with tarfile.open(archive) as tf:
            member = tf.extractfile(exe)
            if member is None:
                raise SystemExit(f"build_wheels: {exe} missing from {archive}")
            with member as src, open(dest, "wb") as out:
                shutil.copyfileobj(src, out)
    dest.chmod(0o755)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("--assets", required=True, type=pathlib.Path)
    parser.add_argument("--out", required=True, type=pathlib.Path)
    opts = parser.parse_args()
    version = opts.version.lstrip("v")
    package_dir = pathlib.Path(__file__).resolve().parent
    opts.out.mkdir(parents=True, exist_ok=True)

    for artifact, ext, plat_tag in PLATFORMS:
        archive = opts.assets / f"agentminutes_{version}_{artifact}.{ext}"
        if not archive.exists():
            raise SystemExit(f"build_wheels: missing release archive {archive}")
        exe = "agentminutes.exe" if artifact.startswith("windows") else "agentminutes"
        with tempfile.TemporaryDirectory() as tmp:
            work = pathlib.Path(tmp) / "pkg"
            work.mkdir()
            for entry in PACKAGE_FILES:
                src = package_dir / entry
                if src.is_dir():
                    shutil.copytree(src, work / entry,
                                    ignore=shutil.ignore_patterns("__pycache__"))
                else:
                    shutil.copy2(src, work / entry)
            pyproject = work / "pyproject.toml"
            stamped = pyproject.read_text().replace('version = "0.0.0"', f'version = "{version}"', 1)
            pyproject.write_text(stamped)
            extract_binary(archive, exe, work / "src" / "agentminutes" / "bin" / exe)

            wheel_dir = pathlib.Path(tmp) / "wheel"
            subprocess.run(
                [sys.executable, "-m", "build", "--wheel", "--outdir", str(wheel_dir), str(work)],
                check=True,
                capture_output=True,
            )
            built = next(wheel_dir.glob("*.whl"))
            subprocess.run(
                [sys.executable, "-m", "wheel", "tags", "--platform-tag", plat_tag, "--remove", str(built)],
                check=True,
                capture_output=True,
            )
            retagged = next(wheel_dir.glob("*.whl"))
            shutil.copy2(retagged, opts.out / retagged.name)
            print(f"build_wheels: {retagged.name}")


if __name__ == "__main__":
    main()
