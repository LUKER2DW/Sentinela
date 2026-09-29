/** The server command that adds the camera; the password is asked there. */
export function addCameraCommand(id: string, host: string, name: string, onvifPort: number): string {
  const env = onvifPort !== 8899 ? `ONVIF_PORT=${onvifPort} ` : "";
  const safeName = name.replace(/["\\$`]/g, "").trim() || id;
  return `sudo ${env}/opt/sentinela/scripts/add-camera.sh ${id} ${host} "${safeName}"`;
}
