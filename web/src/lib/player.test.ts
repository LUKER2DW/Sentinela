import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";
import { setZone } from "./access";
import { LivePlayer, WEBRTC_MAX_DELAY, catchUpRate, persistentlyLate, webrtcRecentlyFailed, type PlayerOptions } from "./player";

vi.mock("./api", () => ({ api: vi.fn() }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((ok, fail) => { resolve = ok; reject = fail; });
  return { promise, resolve, reject };
}

class FakeSocket {
  static OPEN = 1;
  static instances: FakeSocket[] = [];
  readyState = 0;
  binaryType = "";
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((event: { data: string | ArrayBuffer }) => void) | null = null;
  sent: string[] = [];
  constructor(readonly url: string) { FakeSocket.instances.push(this); }
  open() { this.readyState = FakeSocket.OPEN; this.onopen?.(); }
  receive(data: string | ArrayBuffer) { this.onmessage?.({ data }); }
  send(data: string) { this.sent.push(data); }
  close() { this.readyState = 3; }
}

class FakeSourceBuffer extends EventTarget {
  updating = false;
  mode = "";
  ranges: [number, number][] = [];
  appended: ArrayBuffer[] = [];
  get buffered() {
    return {
      length: this.ranges.length,
      start: (i: number) => this.ranges[i][0],
      end: (i: number) => this.ranges[i][1],
    };
  }
  appendBuffer(data: ArrayBuffer) { this.appended.push(data); this.updating = true; }
  remove() { this.updating = true; }
  finish(ranges = this.ranges) {
    this.ranges = ranges;
    this.updating = false;
    this.dispatchEvent(new Event("updateend"));
  }
}

class FakeMediaSource extends EventTarget {
  static instances: FakeMediaSource[] = [];
  static isTypeSupported() { return true; }
  readyState = "closed";
  buffers: FakeSourceBuffer[] = [];
  constructor() { super(); FakeMediaSource.instances.push(this); }
  addSourceBuffer() {
    const sb = new FakeSourceBuffer();
    this.buffers.push(sb);
    return sb;
  }
  open() { this.readyState = "open"; this.dispatchEvent(new Event("sourceopen")); }
  endOfStream() { this.readyState = "ended"; }
}

class FakeTrack {
  enabled = true;
  stop = vi.fn();
  constructor(readonly kind: "audio" | "video") {}
}

class FakeStream {
  constructor(private tracks: FakeTrack[] = []) {}
  addTrack(track: FakeTrack) { this.tracks.push(track); }
  getTracks() { return this.tracks; }
  getAudioTracks() { return this.tracks.filter((t) => t.kind === "audio"); }
}

type TrackEvent = { track: FakeTrack; receiver: { jitterBufferTarget: number | null } };
type CandidateEvent = { candidate: { candidate: string } | null };

class FakePeer {
  static instances: FakePeer[] = [];
  static offers: Promise<RTCSessionDescriptionInit>[] = [];
  connectionState = "new";
  localDescription: RTCSessionDescriptionInit | null = null;
  ontrack: ((event: TrackEvent) => void) | null = null;
  onicecandidate: ((event: CandidateEvent) => void) | null = null;
  onconnectionstatechange: (() => void) | null = null;
  createOffer: ReturnType<typeof vi.fn>;
  addTransceiver = vi.fn();
  setLocalDescription = vi.fn(async (offer: RTCSessionDescriptionInit) => { this.localDescription = offer; });
  setRemoteDescription = vi.fn(async () => {});
  addIceCandidate = vi.fn(async () => {});
  getStats = vi.fn(async () => new Map());
  constructor() {
    FakePeer.instances.push(this);
    const offer = FakePeer.offers.shift() ?? Promise.resolve({ type: "offer", sdp: "current-offer" } as const);
    this.createOffer = vi.fn(() => offer);
  }
  close() { this.connectionState = "closed"; }
}

function fakeVideo() {
  return {
    src: "",
    srcObject: null as unknown,
    currentTime: 0,
    playbackRate: 1,
    disableRemotePlayback: false,
    play: vi.fn(async () => {}),
    load: vi.fn(),
    removeAttribute: vi.fn(),
  };
}

const players: LivePlayer[] = [];
let queueNow = 0;
let getUserMedia: ReturnType<typeof vi.fn>;
let storage: Map<string, string>;

function createPlayer(options: Partial<PlayerOptions> = {}) {
  const video = fakeVideo();
  const status = vi.fn();
  const player = new LivePlayer(video as unknown as HTMLVideoElement, {
    cameraId: "cam1", stream: "main", prefer: "mse", onStatus: status, ...options,
  });
  players.push(player);
  return { player, video, status };
}

async function flush() { for (let i = 0; i < 8; i++) await Promise.resolve(); }

async function connect(player: LivePlayer) {
  player.start();
  await flush();
  const ws = FakeSocket.instances.at(-1)!;
  ws.open();
  await flush();
  return ws;
}

function mseResponse(ws: FakeSocket, open = true) {
  ws.receive(JSON.stringify({ type: "mse", value: 'video/mp4; codecs="hvc1.1.6.L153.B0"' }));
  const ms = FakeMediaSource.instances.at(-1)!;
  if (open) ms.open();
  return ms;
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
  vi.setSystemTime(1_700_000_000_000);
  queueNow = 0;
  storage = new Map();
  FakeSocket.instances = [];
  FakeMediaSource.instances = [];
  FakePeer.instances = [];
  FakePeer.offers = [];
  getUserMedia = vi.fn(async () => new FakeStream([new FakeTrack("audio")]));
  vi.mocked(api).mockReset();
  vi.mocked(api).mockResolvedValue({ url: "/stream/test-ticket" });
  vi.stubGlobal("window", {
    MediaSource: FakeMediaSource,
    isSecureContext: true,
    setTimeout: globalThis.setTimeout,
    clearTimeout: globalThis.clearTimeout,
  });
  vi.stubGlobal("WebSocket", FakeSocket);
  vi.stubGlobal("RTCPeerConnection", FakePeer);
  vi.stubGlobal("RTCRtpReceiver", { getCapabilities: () => ({ codecs: [{ mimeType: "video/H265" }] }) });
  vi.stubGlobal("MediaStream", FakeStream);
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } });
  vi.stubGlobal("location", { protocol: "https:", host: "sentinela.test" });
  vi.stubGlobal("performance", { now: () => queueNow });
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
    removeItem: (key: string) => storage.delete(key),
  });
  vi.spyOn(URL, "createObjectURL").mockImplementation(() => `blob:test-${FakeMediaSource.instances.length}`);
  vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  setZone("lan");
});

afterEach(() => {
  players.splice(0).forEach((p) => p.stop());
  setZone("lan");
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("live player reconnect ownership", () => {
  it("ignores a sourceopen delivered after another stream replaced it", async () => {
    const { player, status } = createPlayer();
    const oldSocket = await connect(player);
    const oldSource = mseResponse(oldSocket, false);
    player.restart();
    await flush();
    const socket = FakeSocket.instances.at(-1)!;
    socket.open();
    const source = mseResponse(socket);
    const count = status.mock.calls.length;

    oldSource.open();
    expect(oldSource.buffers).toHaveLength(0);
    expect(status).toHaveBeenCalledTimes(count);
    socket.receive(new ArrayBuffer(4));
    source.buffers[0].finish([[0, 2]]);
    expect(await player.bufferDelay()).toBeCloseTo(0.3);
  });

  it("does not let old buffer events append or reconnect the replacement stream", async () => {
    const { player } = createPlayer();
    const oldSocket = await connect(player);
    const oldBuffer = mseResponse(oldSocket).buffers[0];
    player.restart();
    await flush();
    const socket = FakeSocket.instances.at(-1)!;
    socket.open();
    const buffer = mseResponse(socket).buffers[0];
    buffer.updating = true;
    const fragment = new ArrayBuffer(4);
    socket.receive(fragment);
    buffer.updating = false;

    oldBuffer.finish();
    oldBuffer.dispatchEvent(new Event("error"));
    expect(buffer.appended).toHaveLength(0);
    expect(player.state).toBe("playing");
    expect(api).toHaveBeenCalledTimes(2);
    buffer.finish();
    expect(buffer.appended).toEqual([fragment]);
  });

  it("reconnects once after an asynchronous decode error, ignoring repeated stale errors", async () => {
    const { player } = createPlayer();
    const socket = await connect(player);
    const buffer = mseResponse(socket).buffers[0];
    buffer.dispatchEvent(new Event("error"));
    buffer.dispatchEvent(new Event("error"));
    expect(player.state).toBe("reconnecting");
    await vi.advanceTimersByTimeAsync(2_000);
    expect(api).toHaveBeenCalledTimes(2);
    expect(FakeSocket.instances).toHaveLength(2);
  });

  it("ignores an old socket open and queued messages after restart", async () => {
    const { player } = createPlayer();
    player.start();
    await flush();
    const oldSocket = FakeSocket.instances[0];
    player.restart();
    await flush();
    const socket = FakeSocket.instances.at(-1)!;
    socket.open();
    const buffer = mseResponse(socket).buffers[0];
    const requests = [...socket.sent];

    oldSocket.open();
    oldSocket.receive(new ArrayBuffer(4));
    oldSocket.receive(JSON.stringify({ type: "error", value: "stale stream" }));
    expect(socket.sent).toEqual(requests);
    expect(buffer.appended).toHaveLength(0);
    expect(player.state).toBe("playing");
  });

  it("does not send a deferred offer from the previous peer over the new socket", async () => {
    const offer = deferred<RTCSessionDescriptionInit>();
    FakePeer.offers.push(offer.promise);
    const { player } = createPlayer({ prefer: "webrtc" });
    await connect(player);
    const oldPeer = FakePeer.instances[0];
    player.restart();
    await flush();
    const socket = FakeSocket.instances.at(-1)!;
    socket.open();
    await flush();
    const requests = [...socket.sent];

    offer.resolve({ type: "offer", sdp: "stale-offer" });
    await flush();
    expect(oldPeer.setLocalDescription).not.toHaveBeenCalled();
    expect(socket.sent).toEqual(requests);
    expect(socket.sent.join()).not.toContain("stale-offer");
  });

  it("stops microphone tracks returned after the player has stopped", async () => {
    const mic = deferred<FakeStream>();
    getUserMedia.mockReturnValueOnce(mic.promise);
    const { player, video } = createPlayer({ talk: true });
    const socket = await connect(player);
    const peer = FakePeer.instances[0];
    player.stop();
    const track = new FakeTrack("audio");
    mic.resolve(new FakeStream([track]));
    await flush();

    expect(track.stop).toHaveBeenCalledOnce();
    expect(peer.createOffer).not.toHaveBeenCalled();
    expect(socket.sent).toHaveLength(0);
    expect(video.srcObject).toBeNull();
    expect(player.state).toBe("stopped");
  });

  it("ignores stale peer tracks, candidates, and connection-state callbacks", async () => {
    const { player, video } = createPlayer({ prefer: "webrtc" });
    await connect(player);
    const oldPeer = FakePeer.instances[0];
    player.restart();
    await flush();
    const socket = FakeSocket.instances.at(-1)!;
    socket.open();
    await flush();
    const peer = FakePeer.instances.at(-1)!;
    peer.ontrack?.({ track: new FakeTrack("video"), receiver: { jitterBufferTarget: null } });
    const stream = video.srcObject;
    const requests = [...socket.sent];

    oldPeer.ontrack?.({ track: new FakeTrack("video"), receiver: { jitterBufferTarget: null } });
    oldPeer.onicecandidate?.({ candidate: { candidate: "stale-candidate" } });
    oldPeer.connectionState = "failed";
    oldPeer.onconnectionstatechange?.();
    expect(video.srcObject).toBe(stream);
    expect(socket.sent).toEqual(requests);
    expect(api).toHaveBeenCalledTimes(2);
    expect(player.peer).toBe(peer);
  });
});

describe("MSE latency and intact frame queues", () => {
  it("catches up after the final append even when no fragments remain queued", async () => {
    const { player, video } = createPlayer();
    const socket = await connect(player);
    const buffer = mseResponse(socket).buffers[0];
    socket.receive(new ArrayBuffer(4));
    video.currentTime = 6;
    video.playbackRate = 1.1;
    buffer.finish([[0, 8]]);

    expect(video.currentTime).toBeCloseTo(7.7);
    expect(video.playbackRate).toBe(1);
    expect(buffer.appended).toHaveLength(1);
    expect(await player.bufferDelay()).toBeCloseTo(0.3);
  });

  it("seeks into the newest buffered range when the playhead falls in a gap", async () => {
    const { player, video } = createPlayer();
    const socket = await connect(player);
    const buffer = mseResponse(socket).buffers[0];
    video.currentTime = 3.6;
    buffer.finish([[0, 1], [4, 5]]);
    expect(video.currentTime).toBeCloseTo(4.7);
  });

  it("preserves every queued fragment in arrival order while within the budget", async () => {
    const { player } = createPlayer();
    const socket = await connect(player);
    const buffer = mseResponse(socket).buffers[0];
    const fragments = [new ArrayBuffer(4), new ArrayBuffer(8), new ArrayBuffer(12)];
    fragments.forEach((fragment) => socket.receive(fragment));
    expect(buffer.appended).toHaveLength(1);
    buffer.finish();
    buffer.finish();
    expect(buffer.appended).toEqual(fragments);
    expect(api).toHaveBeenCalledOnce();
  });

  it.each(["receive", "drain"] as const)("restarts instead of appending a queue older than two seconds on %s", async (trigger) => {
    const { player } = createPlayer();
    const socket = await connect(player);
    const buffer = mseResponse(socket).buffers[0];
    const first = new ArrayBuffer(4);
    socket.receive(first);
    socket.receive(new ArrayBuffer(8));
    queueNow = 2_001;
    if (trigger === "receive") socket.receive(new ArrayBuffer(12));
    else buffer.finish();
    await flush();

    expect(buffer.appended).toEqual([first]);
    expect(api).toHaveBeenCalledTimes(2);
    const freshSocket = FakeSocket.instances.at(-1)!;
    freshSocket.open();
    const freshBuffer = mseResponse(freshSocket).buffers[0];
    const freshInit = new ArrayBuffer(16);
    freshSocket.receive(freshInit);
    expect(freshBuffer.appended).toEqual([freshInit]);
  });

  it("reopens from a fresh stream when pending fragments exceed eight MiB", async () => {
    const { player } = createPlayer();
    const socket = await connect(player);
    const buffer = mseResponse(socket).buffers[0];
    const first = new ArrayBuffer(4);
    socket.receive(first);
    socket.receive(new ArrayBuffer(8 * 1024 * 1024));
    expect(api).toHaveBeenCalledOnce();
    socket.receive(new ArrayBuffer(1));
    await flush();

    expect(buffer.appended).toEqual([first]);
    expect(api).toHaveBeenCalledTimes(2);
    const freshSocket = FakeSocket.instances.at(-1)!;
    freshSocket.open();
    const freshBuffer = mseResponse(freshSocket).buffers[0];
    const freshInit = new ArrayBuffer(16);
    freshSocket.receive(freshInit);
    buffer.finish();
    expect(freshBuffer.appended).toEqual([freshInit]);
  });
});

describe("WebRTC failure cooldown", () => {
  it.each(["available", "blocked"])("retries WebRTC on the same player after its cooldown expires with storage %s", async (storageMode) => {
    if (storageMode === "blocked") {
      const unavailable = () => { throw new Error("storage unavailable"); };
      vi.stubGlobal("localStorage", { getItem: unavailable, setItem: unavailable, removeItem: unavailable });
    }
    const { player } = createPlayer({ prefer: undefined });
    await connect(player);
    const oldPeer = FakePeer.instances[0];
    oldPeer.connectionState = "failed";
    oldPeer.onconnectionstatechange?.();
    await flush();
    const mseSocket = FakeSocket.instances.at(-1)!;
    mseSocket.open();
    mseResponse(mseSocket);
    expect(player.currentTransport).toBe("mse");
    expect(player.state).toBe("playing");

    await vi.advanceTimersByTimeAsync(10 * 60_000 + 1);
    player.restart();
    await flush();
    const rtcSocket = FakeSocket.instances.at(-1)!;
    rtcSocket.open();
    await flush();

    expect(player.currentTransport).toBe("webrtc");
    expect(player.peer).not.toBe(oldPeer);
    expect(FakePeer.instances).toHaveLength(2);
    expect(rtcSocket.sent.some((message) => JSON.parse(message).type === "webrtc/offer")).toBe(true);
  });

  it("expires after ten minutes and ignores invalid future timestamps", () => {
    const now = Date.now();
    storage.set("sentinela.webrtcFailedAt.lan", String(now));
    expect(webrtcRecentlyFailed(now)).toBe(true);
    expect(webrtcRecentlyFailed(now + 10 * 60_000 - 1)).toBe(true);
    expect(webrtcRecentlyFailed(now + 10 * 60_000)).toBe(false);
    expect(webrtcRecentlyFailed(now - 1)).toBe(false);
  });

  it("ignores the previous unscoped week-long failure when selecting a transport", async () => {
    storage.set("sentinela.webrtcFailedAt", String(Date.now()));
    const { player } = createPlayer({ prefer: undefined });
    await connect(player);
    expect(webrtcRecentlyFailed()).toBe(false);
    expect(player.currentTransport).toBe("webrtc");
  });

  it("remembers a public-path failure without disabling WebRTC on the LAN", async () => {
    setZone("public");
    const { player } = createPlayer({ prefer: undefined });
    await connect(player);
    const peer = FakePeer.instances[0];
    peer.connectionState = "failed";
    peer.onconnectionstatechange?.();
    await flush();
    expect(player.currentTransport).toBe("mse");
    expect(webrtcRecentlyFailed()).toBe(true);
    expect(storage.get("sentinela.webrtcFailedAt.public")).toBe(String(Date.now()));
    player.stop();

    setZone("lan");
    expect(webrtcRecentlyFailed()).toBe(false);
    const { player: lan } = createPlayer({ prefer: undefined });
    await connect(lan);
    expect(lan.currentTransport).toBe("webrtc");
  });
});

describe("live-edge guard", () => {
  it("moves off WebRTC only after a sustained large jitter buffer", () => {
    expect(persistentlyLate([1.2, 1.3])).toBe(false);
    expect(persistentlyLate([0.4, 1.2, 1.3, 1.6])).toBe(true);
    expect(persistentlyLate([1.2, 0.8, 1.6])).toBe(false);
    expect(persistentlyLate([1.2, null, 1.6])).toBe(false);
    expect(WEBRTC_MAX_DELAY).toBeLessThanOrEqual(1);
  });
  it("catches up faster the further behind MSE is", () => {
    expect(catchUpRate(0.2)).toBe(1);
    expect(catchUpRate(0.5)).toBeGreaterThan(1);
    expect(catchUpRate(1.0)).toBeGreaterThan(catchUpRate(0.5));
  });
});
