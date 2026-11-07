import { Avatar } from "./Avatar";

// Drawn file icons for the Cloud (round 6, owner): a page with a folded
// corner, a glyph for what is inside and a coloured label with the
// extension; folders are two-tone. Colours come from CSS (.fi-*), so both
// themes work.

export type FileType =
  | "folder"
  | "shared"
  | "user"
  | "trash"
  | "image"
  | "video"
  | "audio"
  | "pdf"
  | "doc"
  | "sheet"
  | "slides"
  | "archive"
  | "code"
  | "text"
  | "apk"
  | "file";

const BY_EXT: [RegExp, FileType][] = [
  [/\.(jpe?g|png|gif|webp|heic|heif|bmp|avif|svg|tiff?)$/i, "image"],
  [/\.(mp4|mov|mkv|webm|avi|m4v|3gp|ts)$/i, "video"],
  [/\.(mp3|m4a|aac|flac|wav|ogg|opus|wma|mid|midi)$/i, "audio"],
  [/\.pdf$/i, "pdf"],
  [/\.(docx?|odt|rtf|pages)$/i, "doc"],
  [/\.(xlsx?|ods|csv|tsv|numbers)$/i, "sheet"],
  [/\.(pptx?|odp|key)$/i, "slides"],
  [/\.(zip|rar|7z|tar|gz|tgz|bz2|xz|iso)$/i, "archive"],
  [/\.apk$/i, "apk"],
  [/\.(js|ts|tsx|jsx|go|py|java|c|cpp|h|cs|rs|sh|ps1|bat|html?|css|json|xml|ya?ml|toml|ini|conf|sql)$/i, "code"],
  [/\.(txt|md|log|srt|vtt|nfo)$/i, "text"],
];

export function fileType(name: string, dir: boolean, opts?: { shared?: boolean; trash?: boolean; user?: boolean }): FileType {
  if (dir) return opts?.trash ? "trash" : opts?.shared ? "shared" : opts?.user ? "user" : "folder";
  return BY_EXT.find(([re]) => re.test(name))?.[1] ?? "file";
}

const extOf = (n: string) => {
  const i = n.lastIndexOf(".");
  return i > 0 && n.length - i <= 5 ? n.slice(i + 1).toUpperCase() : "";
};

const PAGE = "M10 3.5h14.5l8.5 8.5v23a2.5 2.5 0 0 1-2.5 2.5h-20.5a2.5 2.5 0 0 1-2.5-2.5v-29a2.5 2.5 0 0 1 2.5-2.5z";
const FOLD = "M24.5 3.5v6a2.5 2.5 0 0 0 2.5 2.5h6";

function Glyph({ t }: { t: FileType }) {
  switch (t) {
    case "image":
      return (
        <>
          <circle cx="15" cy="13.5" r="2.2" className="fi-fill" />
          <path d="M10.5 23l5-5.5 3.5 3.5 2.5-2.5 5 4.5z" className="fi-fill" />
        </>
      );
    case "video":
      return <path d="M15.5 11.5v10l8.5-5z" className="fi-fill" />;
    case "audio":
      return <path d="M17 21.5V11.5l7-1.8v9.8M17 21.5a2.2 2.2 0 1 1-4.4 0 2.2 2.2 0 0 1 4.4 0zM24 19.5a2.2 2.2 0 1 1-4.4 0 2.2 2.2 0 0 1 4.4 0z" className="fi-line" />;
    case "doc":
    case "text":
      return <path d="M12 12h13M12 16h13M12 20h9" className="fi-line" />;
    case "sheet":
      return <path d="M11.5 11h14v11h-14zM11.5 14.7h14M11.5 18.3h14M16.5 11v11" className="fi-line" />;
    case "slides":
      return (
        <>
          <rect x="11" y="11" width="15" height="10" rx="1.5" className="fi-line" />
          <path d="M14 18l3-3 2 2 2.5-2.5" className="fi-line" />
        </>
      );
    case "archive":
      return <path d="M18 3.5v3h2.5v3H18v3h2.5v3H18v3h2.5" className="fi-line" />;
    case "code":
      return <path d="M15 12.5l-4 4 4 4M22 12.5l4 4-4 4M20 11l-3 11" className="fi-line" />;
    case "apk":
      return <path d="M12.5 20.5a6 6 0 0 1 12 0zM14.5 13.5l1.5 2.2M22.5 13.5l-1.5 2.2M16.2 18h.01M20.8 18h.01" className="fi-line" />;
    case "pdf":
      return <path d="M12.5 21c3-1.5 5.5-6.5 5.5-9.2 0-1.8-1.6-1.8-1.6 0 0 3.5 4.6 7.7 8.6 7.2 1.5-.2 1.2-1.7-.6-1.5-3.5.4-8.6 2.6-11.9 3.5z" className="fi-line" />;
    default:
      return <path d="M12 13h13M12 17h9" className="fi-line" />;
  }
}

/** One file or folder icon, 40×40 design. `owner` puts that person's photo on a folder. */
export function FileIcon({ type, name, owner, big }: { type: FileType; name: string; owner?: string; big?: boolean }) {
  const folder = type === "folder" || type === "shared" || type === "user" || type === "trash";
  const ext = folder ? "" : extOf(name);
  return (
    <span className={`fi fi-${type}${big ? " big" : ""}`} aria-hidden="true">
      <svg viewBox="0 0 40 40">
        {folder ? (
          <>
            <path className="fi-back" d="M3.5 10.5a3 3 0 0 1 3-3h8.2a3 3 0 0 1 2.2 1l2.3 2.5h14.3a3 3 0 0 1 3 3v2H3.5z" />
            <path className="fi-front" d="M3.5 15a3 3 0 0 1 3-3h27a3 3 0 0 1 3 3v16.5a3 3 0 0 1-3 3h-27a3 3 0 0 1-3-3z" />
            {type === "shared" && (
              <path
                className="fi-mark"
                d="M17 22.5a2.6 2.6 0 1 0 0-.01M12.5 30.5a4.5 4.5 0 0 1 9 0M24.5 21.5a2.2 2.2 0 1 0 0-.01M23 26.4a4 4 0 0 1 5.5 3.6"
              />
            )}
            {type === "trash" && <path className="fi-mark" d="M14.5 19.5h11M18 19.5v-1.5h4v1.5M15.5 19.5l.8 10h7.4l.8-10" />}
          </>
        ) : (
          <>
            <path className="fi-page" d={PAGE} />
            <path className="fi-fold" d={FOLD} />
            <g className="fi-glyph">
              <Glyph t={type} />
            </g>
            {ext && (
              <>
                <rect className="fi-band" x="4" y="25" width={Math.max(15, ext.length * 5.2 + 7)} height="9" rx="2" />
                <text className="fi-ext" x={4 + Math.max(15, ext.length * 5.2 + 7) / 2} y="31.8" textAnchor="middle">
                  {ext}
                </text>
              </>
            )}
          </>
        )}
      </svg>
      {type === "user" && owner && <Avatar name={owner} className="fi-avatar" />}
    </span>
  );
}
