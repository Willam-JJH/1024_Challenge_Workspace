"""uploads 卷的备份与恢复演练。

用 alpine 容器当工具箱：往源卷写几个文件（含子目录与非默认属主），
tar 打包到宿主机临时目录，解到新卷后逐文件比对内容与属主，最后清理。

只动临时卷，不碰 `challenge_uploads` 生产卷：
    python tests/parity/uploads_backup.py
"""

import os
import subprocess
import sys
import tempfile

IMAGE = os.environ.get("TOOLBOX_IMAGE", "alpine:latest")
SRC = os.environ.get("SRC_VOLUME", "ubench-uploads-src")
DST = os.environ.get("DST_VOLUME", "ubench-uploads-dst")

checks = []


def check(name, ok, extra=""):
    checks.append(bool(ok))
    print(("PASS " if ok else "FAIL ") + name + (f"  {extra}" if extra else ""))


def docker(*args, check_code=True):
    result = subprocess.run(["docker", *args], capture_output=True, text=True)
    if check_code and result.returncode != 0:
        raise SystemExit(f"docker {' '.join(args)} 失败:\n{result.stderr}")
    return result.stdout.strip()


def in_volume(volume, script, backup_dir=None):
    args = ["run", "--rm", "-v", f"{volume}:/data"]
    if backup_dir:
        args += ["-v", f"{backup_dir}:/backup"]
    args += [IMAGE, "sh", "-c", script]
    return docker(*args)


def listing(volume):
    return in_volume(volume, "cd /data && find . -type f | sort | xargs -r md5sum"
                     " && find . -type f | sort | xargs -r stat -c '%n %u %g'")


print("--- 准备源卷 ---")
for volume in (SRC, DST):
    docker("volume", "rm", "-f", volume, check_code=False)
    docker("volume", "create", volume)

in_volume(SRC, """
set -e
mkdir -p /data/assets/2026
printf 'hello uploads' > /data/assets/hello.txt
head -c 200000 /dev/zero | tr '\\0' 'a' > /data/assets/big.bin
printf 'nested' > /data/assets/2026/nested.txt
chown -R 10001:10001 /data
""")
check("源卷写入成功", "hello uploads" in in_volume(SRC, "cat /data/assets/hello.txt"))

with tempfile.TemporaryDirectory() as backup_dir:
    print(f"--- 备份到 {backup_dir} ---")
    in_volume(SRC, "tar czf /backup/uploads.tgz -C /data .", backup_dir=backup_dir)
    archive = os.path.join(backup_dir, "uploads.tgz")
    size = os.path.getsize(archive) if os.path.exists(archive) else 0
    check("备份包已生成且非空", size > 0, f"{size} bytes")

    print("--- 恢复到新卷 ---")
    in_volume(DST, "tar xzf /backup/uploads.tgz -C /data", backup_dir=backup_dir)
    check("恢复后文件清单与内容一致", listing(SRC) == listing(DST))
    owner = in_volume(DST, "stat -c '%u:%g' /data/assets/hello.txt")
    check("恢复后属主保持不变", owner == "10001:10001", owner)

print("--- 清理 ---")
for volume in (SRC, DST):
    docker("volume", "rm", "-f", volume, check_code=False)

print(f"\n{sum(checks)}/{len(checks)} checks passed")
if not all(checks):
    sys.exit(1)
