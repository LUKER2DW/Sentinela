import { afterEach, describe, expect, it, vi } from "vitest";
import { createPlayerFullscreen, EXIT_PLAYER_FULLSCREEN } from "./playerFullscreen";
import { installBackHandler } from "./back";

class Classes {
  names = new Set<string>();
  toggle(name: string, force: boolean) {
    if (force) this.names.add(name);
    else this.names.delete(name);
  }
  remove(name: string) { this.names.delete(name); }
  contains(name: string) { return this.names.has(name); }
}

function setup() {
  const el = Object.assign(new EventTarget(), { classList: new Classes(), requestFullscreen: vi.fn<() => Promise<void>>() });
  el.requestFullscreen.mockRejectedValue(new Error("Requires a gesture"));
  const doc = Object.assign(new EventTarget(), {
    fullscreenElement: null as unknown,
    body: { classList: new Classes() },
    querySelector: vi.fn(() => el.classList.contains("viewport-fullscreen") ? el : null),
    exitFullscreen: vi.fn(async () => { doc.fullscreenElement = null; doc.dispatchEvent(new Event("fullscreenchange")); }),
  });
  vi.stubGlobal("document", doc);
  const change = vi.fn();
  const controller = createPlayerFullscreen(el as unknown as HTMLElement, change);
  return { el, doc, controller, change };
}

afterEach(() => vi.unstubAllGlobals());

describe("live fullscreen without replacing the video", () => {
  it("enters on landscape, exits on portrait and never requests an orientation lock", () => {
    const { el, doc, controller, change } = setup();
    controller.setAutomatic(true);
    expect(el.classList.contains("viewport-fullscreen")).toBe(true);
    expect(doc.body.classList.contains("live-fullscreen")).toBe(true);
    expect(el.requestFullscreen).not.toHaveBeenCalled();
    controller.setAutomatic(false);
    expect(change).toHaveBeenLastCalledWith(false);
    expect(doc.body.classList.contains("live-fullscreen")).toBe(false);
    controller.destroy();
  });

  it("Back dismisses landscape fullscreen until the phone rotates again", () => {
    const { el, controller, change } = setup();
    vi.stubGlobal("window", {});
    controller.setAutomatic(true);
    installBackHandler();
    expect(window.sentinelaBack?.(true)).toBe(true);
    expect(change).toHaveBeenLastCalledWith(false);
    controller.setAutomatic(true);
    expect(el.classList.contains("viewport-fullscreen")).toBe(false);
    controller.setAutomatic(false);
    controller.setAutomatic(true);
    expect(change).toHaveBeenLastCalledWith(true);
    controller.destroy();
  });

  it("keeps the viewport fallback when manual native fullscreen is rejected", async () => {
    const { el, controller, change } = setup();
    controller.toggle();
    await Promise.resolve();
    expect(el.requestFullscreen).toHaveBeenCalledTimes(1);
    expect(change).toHaveBeenLastCalledWith(true);
    el.dispatchEvent(new Event(EXIT_PLAYER_FULLSCREEN));
    expect(change).toHaveBeenLastCalledWith(false);
    controller.destroy();
  });

  it("navigation cleans up the expanded viewport and its listeners", () => {
    const { el, doc, controller, change } = setup();
    controller.setAutomatic(true);
    controller.destroy();
    expect(doc.body.classList.contains("live-fullscreen")).toBe(false);
    const calls = change.mock.calls.length;
    el.dispatchEvent(new Event(EXIT_PLAYER_FULLSCREEN));
    doc.dispatchEvent(new Event("fullscreenchange"));
    expect(change).toHaveBeenCalledTimes(calls);
  });
});
