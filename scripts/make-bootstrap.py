#!/usr/bin/env python3
# Build a device install bootstrap zip {MANIFEST, pkg.bar} from a packaged .bar.
#
# The on-device installer (/var/rootdata/installer) reads a plain MANIFEST at the
# root of the zip for its job id (Package-Id), then installs pkg.bar. The app's real
# identity (upgrade-in-place key) comes from the .bar's bar-descriptor <id>, not this
# MANIFEST — but a readable Package-Id keeps the installer from falling back to
# "unknown_package" (which litters /var/addons). The one structural detail that
# matters: MANIFEST is the FIRST entry and carries a fixed 1980 timestamp, matching
# the bootstraps the installer has accepted; a current timestamp made it miss the id.
#
# Usage: make-bootstrap.py <path-to.bar> <version e.g. 0.1.0.24> [out.zip]
import sys, zipfile

def main():
    if len(sys.argv) < 3:
        print("usage: make-bootstrap.py <bar> <version> [out.zip]", file=sys.stderr); return 2
    bar, version = sys.argv[1], sys.argv[2]
    pid = "proxyresearch-" + version.replace(".", "-")
    out = sys.argv[3] if len(sys.argv) > 3 else bar.rsplit(".bar", 1)[0] + f"-bootstrap-{version}.zip"
    manifest = (f"Package-Id: {pid}\n"
                f"Package-Name: BB10 Proxy Research\n"
                f"Package-Version: {version}\n").encode()
    z = zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED)
    mi = zipfile.ZipInfo("MANIFEST", date_time=(1980, 1, 1, 0, 0, 0))  # fixed ts: installer-friendly
    mi.compress_type = zipfile.ZIP_DEFLATED
    z.writestr(mi, manifest)
    z.write(bar, "pkg.bar")
    z.close()
    print(out)

if __name__ == "__main__":
    sys.exit(main())
