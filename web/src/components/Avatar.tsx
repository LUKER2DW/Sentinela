import { useState, useSyncExternalStore } from "react";
import { api } from "../lib/api";

import { t } from "../lib/i18n";
// Profile photos: /api/avatars/{username}, a letter when there is none. A
// shared version number busts the browser cache after this device changes
// its own picture (other people's pictures refresh within 5 minutes).
let version = Date.now();
const subs = new Set<() => void>();
const bump = () => {
  version = Date.now();
  subs.forEach((f) => f());
};
function useVersion() {
  return useSyncExternalStore(
    (f) => {
      subs.add(f);
      return () => subs.delete(f);
    },
    () => version,
  );
}

// Names already known to have no picture, so lists do not ask again.
const missing = new Set<string>();

export function Avatar({ name, className = "", title }: { name: string; className?: string; title?: string }) {
  const v = useVersion();
  const key = `${name}@${v}`;
  const [failed, setFailed] = useState<string | null>(missing.has(key) ? key : null);
  const letter = (name || "?").slice(0, 1).toUpperCase();
  return (
    <span className={`avatar ${className}`} aria-hidden={title ? undefined : true} title={title}>
      {failed === key || !name ? (
        letter
      ) : (
        <img
          src={`/api/avatars/${encodeURIComponent(name)}?v=${v}`}
          alt=""
          loading="lazy"
          decoding="async"
          onError={() => {
            missing.add(key);
            setFailed(key);
          }}
        />
      )}
    </span>
  );
}

/**
 * Crops the picture to a centred square, shrinks it to 256 px and encodes
 * it here in the browser (WebP, or JPEG where WebP encoding is missing), so
 * the server never decodes images. Then uploads it.
 */
export async function uploadAvatar(file: File): Promise<void> {
  const bitmap = await createImageBitmap(file).catch(() => {
    throw new Error(t("Could not open this picture."));
  });
  const side = Math.min(bitmap.width, bitmap.height);
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = 256;
  canvas.getContext("2d")!.drawImage(bitmap, (bitmap.width - side) / 2, (bitmap.height - side) / 2, side, side, 0, 0, 256, 256);
  bitmap.close();
  const encode = (type: string) => new Promise<Blob | null>((ok) => canvas.toBlob(ok, type, 0.86));
  let blob = await encode("image/webp");
  if (!blob || blob.type !== "image/webp") blob = await encode("image/jpeg");
  if (!blob) throw new Error(t("Could not prepare the picture."));
  await api("POST", "/api/me/avatar", blob);
  bump();
}

export async function removeAvatar(): Promise<void> {
  await api("DELETE", "/api/me/avatar");
  bump();
}
