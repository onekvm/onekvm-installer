This directory may contain verified BSDIFF40 development packages generated
from existing OE/kas signed SPL outputs. The tracked packages are embedded in
the arm64 installer release. The Go repair provider pins each supported
source, patch and complete target hash; missing or changed payloads disable
repair before any write. Never use boot-rom-test.bin.
