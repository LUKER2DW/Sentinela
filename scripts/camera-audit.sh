#!/usr/bin/env bash
# Read-only audit of an XMEye/Xiongmai IP camera.
# Usage: ./scripts/camera-audit.sh <camera-ip> [username]
# Password is read silently (or from SENTINELA_CAMERA_PASSWORD) and never written to disk.
# Output: audit-out/<ip>-<timestamp>/ (git-ignored). Review and redact before sharing.
set -euo pipefail

HOST="${1:?usage: $0 <camera-ip> [username]}"
USER_NAME="${2:-admin}"
if [[ -z "${SENTINELA_CAMERA_PASSWORD:-}" ]]; then
  read -r -s -p "Camera password for ${USER_NAME}@${HOST}: " SENTINELA_CAMERA_PASSWORD; echo
fi
# XMEye firmwares usually serve ONVIF on 8899, some on 80. Override with ONVIF_PORT.
ONVIF_PORT="${ONVIF_PORT:-}"
export SENTINELA_CAMERA_PASSWORD HOST USER_NAME ONVIF_PORT

OUT="audit-out/${HOST}-$(date +%Y%m%dT%H%M%S)"
mkdir -p "$OUT"; chmod 700 audit-out "$OUT"
log(){ printf '\n== %s\n' "$*" | tee -a "$OUT/summary.txt"; }
have(){ command -v "$1" >/dev/null 2>&1; }

log "1. Reachability"
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) PING_ARGS=(-n 2 -w 2000) ;; *) PING_ARGS=(-c 2 -W 2) ;; esac
ping "${PING_ARGS[@]}" "$HOST" 2>&1 | tail -3 | tee -a "$OUT/summary.txt" || true

log "2. Open TCP ports (common XMEye set)"
PORTS="23 80 443 554 8000 8080 8899 9527 9530 34567 34568 34599"
if have nmap; then
  nmap -Pn -sT -sV --version-light -p "${PORTS// /,}" "$HOST" -oN "$OUT/nmap.txt" >/dev/null 2>&1 || true
  grep -E '^[0-9]+/tcp' "$OUT/nmap.txt" | tee -a "$OUT/summary.txt" || true
else
  for p in $PORTS; do
    if timeout 2 bash -c "echo > /dev/tcp/$HOST/$p" 2>/dev/null; then echo "$p/tcp open"; fi
  done | tee -a "$OUT/summary.txt"
fi
echo "NOTE: 23 (telnet) or 9527 (debug console) open = serious risk; isolate camera." | tee -a "$OUT/summary.txt"

log "3. HTTP headers (web UI)"
curl -s -m 5 -I "http://$HOST/" | tee "$OUT/http-headers.txt" | grep -Ei '^(HTTP|server|www-authenticate)' | tee -a "$OUT/summary.txt" || true

log "4. ONVIF + DVRIP probes (python)"
python3 - "$OUT" <<'PY' | tee -a "$OUT/summary.txt"
import base64, datetime, hashlib, json, os, re, socket, struct, sys, urllib.request
out = sys.argv[1]; host = os.environ["HOST"]; user = os.environ["USER_NAME"]; pw = os.environ["SENTINELA_CAMERA_PASSWORD"]
REDACT = re.compile(r'("?(SerialNo|SerialNumber|MAC|HardwareId|Mac|Password|PassWord)"?\s*[:>]\s*"?)([^"<,}]+)', re.I)
def redact(s): return REDACT.sub(lambda m: m.group(1) + "REDACTED", s)
def save(name, text):
    with open(os.path.join(out, name), "w") as f: f.write(redact(text))

# ---------- ONVIF ----------
def onvif(path, body, auth=True):
    sec = ""
    if auth:
        nonce = os.urandom(16); created = datetime.datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%SZ")
        digest = base64.b64encode(hashlib.sha1(nonce + created.encode() + pw.encode()).digest()).decode()
        sec = ('<s:Header><Security xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">'
               f'<UsernameToken><Username>{user}</Username>'
               '<Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">'
               f'{digest}</Password><Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">'
               f'{base64.b64encode(nonce).decode()}</Nonce><Created xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">'
               f'{created}</Created></UsernameToken></Security></s:Header>')
    env = f'<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">{sec}<s:Body>{body}</s:Body></s:Envelope>'
    req = urllib.request.Request(f"http://{host}:{onvif_port}{path}", env.encode(), {"Content-Type": "application/soap+xml"})
    try:
        r = urllib.request.urlopen(req, timeout=6).read().decode(errors="replace")
        # Some firmwares answer unknown paths with an HTML 404 page and HTTP 200.
        return r if "Envelope" in r else "ERROR: not a SOAP response: " + r[:80].replace("\n", " ")
    except urllib.error.HTTPError as e:
        return f"HTTP {e.code}: " + e.read().decode(errors="replace")[:500]
    except Exception as e:
        return f"ERROR: {e}"

DS = 'xmlns="http://www.onvif.org/ver10/device/wsdl"'; MS = 'xmlns="http://www.onvif.org/ver10/media/wsdl"'
onvif_port = os.environ.get("ONVIF_PORT", "")
if not onvif_port:
    for onvif_port in ("8899", "80"):
        if "Envelope" in onvif("/onvif/device_service", f"<GetSystemDateAndTime {DS}/>", False): break
print(f"ONVIF port: {onvif_port}")
calls = [
  ("onvif-datetime.xml", "/onvif/device_service", f"<GetSystemDateAndTime {DS}/>", False),
  ("onvif-devinfo.xml", "/onvif/device_service", f"<GetDeviceInformation {DS}/>", True),
  ("onvif-capabilities.xml", "/onvif/device_service", f"<GetCapabilities {DS}><Category>All</Category></GetCapabilities>", True),
  ("onvif-services.xml", "/onvif/device_service", f"<GetServices {DS}><IncludeCapability>false</IncludeCapability></GetServices>", True),
  ("onvif-profiles.xml", "/onvif/media_service", f"<GetProfiles {MS}/>", True),
]
for name, path, body, auth in calls:
    r = onvif(path, body, auth); save(name, r)
    ok = "OK" if "Fault" not in r and not r.startswith(("HTTP", "ERROR")) else r[:120].replace("\n", " ")
    print(f"ONVIF {name[6:-4]:13s}: {ok}")
di = open(os.path.join(out, "onvif-devinfo.xml")).read()
for tag in ("Manufacturer", "Model", "FirmwareVersion", "HardwareId"):
    m = re.search(rf"<[^>]*{tag}>([^<]*)<", di)
    if m and tag != "HardwareId": print(f"  {tag}: {m.group(1)}")
prof = open(os.path.join(out, "onvif-profiles.xml")).read()
tokens = re.findall(r'Profiles[^>]*token="([^"]+)"', prof)
print("  profiles:", tokens)
for enc, w, h in re.findall(r"<[^>]*Encoding>(\w+)<.*?<[^>]*Width>(\d+)<.*?<[^>]*Height>(\d+)<", prof, re.S):
    print(f"  video encoder: {enc} {w}x{h}")
uris = []
for t in tokens[:2]:
    r = onvif("/onvif/media_service", f'<GetStreamUri {MS}><StreamSetup><Stream xmlns="http://www.onvif.org/ver10/schema">RTP-Unicast</Stream>'
              f'<Transport xmlns="http://www.onvif.org/ver10/schema"><Protocol>RTSP</Protocol></Transport></StreamSetup>'
              f'<ProfileToken>{t}</ProfileToken></GetStreamUri>')
    m = re.search(r"<[^>]*Uri>([^<]+)<", r)
    if m:
        u = m.group(1).replace("&amp;", "&"); uris.append(u)
        print(f"  stream uri [{t}]: {redact(re.sub('password=[^&_]*', 'password=REDACTED', u))}")
if not uris:
    print("  no ONVIF stream URIs; falling back to XMEye RTSP convention (main, sub)")
    uris = [f"rtsp://{host}:554/user={user}&password={pw}&channel=1&stream={i}.sdp?real_stream" for i in (0, 1)]
with open(os.path.join(out, ".uris"), "w") as f: f.write("\n".join(uris))
caps = open(os.path.join(out, "onvif-capabilities.xml")).read()
print("  PTZ service:", "yes" if "PTZ" in caps and "XAddr" in caps.split("PTZ",1)[1][:400] else "no/unknown")
print("  Events service:", "yes" if "Events" in caps else "no/unknown")

# ---------- DVRIP (read-only) ----------
def sofia(p):
    h = hashlib.md5(p.encode()).digest(); s = ""
    for i in range(8):
        n = (h[2*i] + h[2*i+1]) % 62
        s += chr(n + 48 if n < 10 else n + 55 if n < 36 else n + 61)
    return s
class DVR:
    def __init__(s): s.sock = socket.create_connection((host, 34567), timeout=6); s.sid = 0; s.seq = 0
    def req(s, msg, data):
        payload = json.dumps(data).encode() + b"\x0a\x00"
        s.sock.sendall(struct.pack("<BB2xIIBBHI", 0xFF, 0, s.sid, s.seq, 0, 0, msg, len(payload)) + payload); s.seq += 1
        hdr = b""
        while len(hdr) < 20: hdr += s.sock.recv(20 - len(hdr))
        _, _, sid, _, _, _, rmsg, ln = struct.unpack("<BB2xIIBBHI", hdr)
        body = b""
        while len(body) < ln: body += s.sock.recv(ln - len(body))
        return rmsg, json.loads(body.rstrip(b"\x00\x0a").decode(errors="replace") or "{}")
try:
    d = DVR()
    for enc in ("MD5",):
        _, r = d.req(1000, {"EncryptType": enc, "LoginType": "DVRIP-Web", "PassWord": sofia(pw), "UserName": user})
    ret = r.get("Ret"); print(f"DVRIP login: Ret={ret} ({'OK' if ret in (100, 515) else 'FAILED'})")
    if ret in (100, 515):
        d.sid = int(r.get("SessionID", "0x0"), 16)
        sidhex = "0x%08X" % d.sid
        for msg, name in ((1020, "SystemInfo"), (1360, "SystemFunction"), (1042, "Camera.Param"), (1042, "Simplify.Encode"),
                          (1042, "Uart.PTZ"), (1042, "NetWork.NetCommon"), (1042, "NetWork.Nat"), (1042, "General.General")):
            try:
                _, r = d.req(msg, {"Name": name, "SessionID": sidhex})
                save(f"dvrip-{name}.json", json.dumps(r, indent=2))
                print(f"  {name:18s}: Ret={r.get('Ret')}")
            except Exception as e:
                print(f"  {name:18s}: ERROR {e}")
        try:
            si = json.load(open(os.path.join(out, "dvrip-SystemInfo.json"))).get("SystemInfo", {})
            for k in ("DeviceModel", "SoftWareVersion", "BuildTime", "HardWare", "VideoInChannel", "AudioInChannel", "TalkInChannel"):
                if k in si: print(f"  {k}: {si[k]}")
        except Exception: pass
        try:
            nat = json.load(open(os.path.join(out, "dvrip-NetWork.Nat.json"))).get("NetWork.Nat", {})
            print(f"  Cloud/P2P (NetWork.Nat) enabled: {nat.get('NatEnable')}")
        except Exception: pass
except Exception as e:
    print(f"DVRIP: ERROR {e}")
PY

log "5. Stream probe (ffprobe, 10 s each)"
if have ffprobe && [[ -s "$OUT/.uris" ]]; then
  i=0
  # `|| [[ -n $uri ]]` keeps the last line when the file has no trailing newline;
  # ffprobe gets </dev/null so it cannot swallow the remaining URIs.
  while read -r uri || [[ -n "$uri" ]]; do
    [[ -z "$uri" ]] && continue
    authuri="${uri/rtsp:\/\//rtsp://${USER_NAME}:${SENTINELA_CAMERA_PASSWORD}@}"
    echo "-- stream $i" | tee -a "$OUT/summary.txt"
    ffprobe -v error -rtsp_transport tcp -analyzeduration 5M -probesize 5M \
      -show_entries stream=codec_type,codec_name,profile,width,height,avg_frame_rate,bit_rate,sample_rate,channels \
      -of compact "$authuri" </dev/null 2>&1 | python3 -c 'import os,sys; sys.stdout.write(sys.stdin.read().replace(os.environ["SENTINELA_CAMERA_PASSWORD"],"REDACTED"))' | tee "$OUT/ffprobe-$i.txt" | tee -a "$OUT/summary.txt"
    # GOP / keyframe interval, real fps and video bitrate over ~10 s of packets
    ffprobe -v error -rtsp_transport tcp -select_streams v:0 -read_intervals "%+10" -show_entries packet=pts_time,size,flags -of csv \
      "$authuri" </dev/null 2>/dev/null | awk -F, '
        {n++; bytes+=$3; if (first=="") first=$2; lastp=$2}
        $4 ~ /K/ {if (last!="") ki[sprintf("%.2f", $2-last)]++; last=$2}
        END {for (k in ki) printf "keyframe interval: %ss (x%d)\n", k, ki[k];
             d=lastp-first; if (d>0) printf "measured: %.1f fps, %.0f kbit/s video over %.1fs\n", (n-1)/d, bytes*8/d/1000, d}' \
      | tee -a "$OUT/summary.txt"
    i=$((i+1))
  done < "$OUT/.uris"
else
  echo "ffprobe missing or no ONVIF stream URIs; skipped." | tee -a "$OUT/summary.txt"
fi
rm -f "$OUT/.uris"

log "Done. Review $OUT/summary.txt, then copy the relevant values into docs/CAMERA.md §3."
unset SENTINELA_CAMERA_PASSWORD
