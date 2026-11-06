#!/usr/bin/env python3
import hashlib, json, lzma, os, sys
from datetime import date

img, repo, tag = sys.argv[1:4]
INIT = os.environ.get('INIT_FORMAT', 'cloudinit-rpi')

down = hashlib.sha256()
with open(img, 'rb') as f:
    for b in iter(lambda: f.read(1 << 20), b''):
        down.update(b)
ext, size = hashlib.sha256(), 0
with lzma.open(img) as f:
    for b in iter(lambda: f.read(1 << 20), b''):
        ext.update(b); size += len(b)

json.dump({'os_list': [{
    'name': 'island',
    'description': 'island',
    'url': f'https://github.com/{repo}/releases/download/{tag}/{os.path.basename(img)}',
    'icon': f'https://raw.githubusercontent.com/{repo}/{tag}/image/icon.png',
    'website': f'https://github.com/{repo}',
    'release_date': date.today().isoformat(),
    'extract_size': size,
    'extract_sha256': ext.hexdigest(),
    'image_download_size': os.path.getsize(img),
    'image_download_sha256': down.hexdigest(),
    'init_format': INIT,
    'devices': ['pi3-64bit', 'pi4-64bit', 'pi5-64bit'],
}]}, sys.stdout, indent=2)
print()
