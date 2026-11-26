// Camera ids and per-account camera access. A removed camera's id is never
// given to another camera (the server keeps the list), and an account's
// stored camera list may still name removed cameras: what the dialogs show
// and save is always worked out against the cameras configured now.

/** The first "camN" from n on that is taken neither by the draft nor by a removed camera. */
export function nextCameraId(n: number, taken: (id: string) => boolean): string {
  while (taken(`cam${n}`)) n++;
  return `cam${n}`;
}

/** The account's cameras that are configured now, in the house's order. */
export function configuredGrant(grant: string[], configured: string[]): string[] {
  return configured.filter((id) => grant.includes(id));
}

/**
 * What the camera access dialog saves: [] (every camera, now and later)
 * when every configured camera is ticked or none is, else the ticked
 * configured ids. Compared as sets: a removed camera still on the account
 * never counts as one of the ticked ones.
 */
export function grantToSave(picked: string[], configured: string[]): string[] {
  const ticked = configuredGrant(picked, configured);
  return ticked.length === configured.length ? [] : ticked;
}

/** Whether two selections tick the same configured cameras. */
export function sameSelection(a: string[], b: string[], configured: string[]): boolean {
  const x = configuredGrant(a, configured);
  const y = configuredGrant(b, configured);
  return x.length === y.length && x.every((id) => y.includes(id));
}
