/** Copies text to the clipboard; resolves true when it worked.
 *
 *  `navigator.clipboard` only exists in secure contexts (https, localhost):
 *  not on `http://` over the home network, and it can be refused inside the
 *  Android WebView. The fallback selects a hidden textarea and runs the
 *  older `execCommand("copy")`, which both accept after a tap. */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // fall through to the older way
  }
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.cssText = "position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;";
  const active = document.activeElement as HTMLElement | null;
  document.body.appendChild(ta);
  try {
    ta.focus();
    ta.select();
    ta.setSelectionRange(0, text.length);
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    document.body.removeChild(ta);
    active?.focus?.();
  }
}
