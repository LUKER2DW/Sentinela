import { describe, expect, it } from "vitest";
import { addCameraCommand } from "./camera";

describe("addCameraCommand", () => {
  it("uses the default ONVIF port silently", () => {
    expect(addCameraCommand("cam2", "192.168.1.21", "Fundo", 8899)).toBe('sudo /opt/sentinela/scripts/add-camera.sh cam2 192.168.1.21 "Fundo"');
  });
  it("passes another port and strips shell-special characters", () => {
    expect(addCameraCommand("cam3", "10.0.0.5", 'Ga"r$age`', 80)).toBe('sudo ONVIF_PORT=80 /opt/sentinela/scripts/add-camera.sh cam3 10.0.0.5 "Garage"');
  });
  it("falls back to the id as name", () => {
    expect(addCameraCommand("cam2", "10.0.0.5", "  ", 8899)).toContain('"cam2"');
  });
});
