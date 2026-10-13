// Saving a message's text as a file.

const pad = (n: number) => String(n).padStart(2, '0');

// '<room>-<YYYYMMDD>-<HHmm>.md', the date and time local. room is NFKC
// normalized, every run of characters other than letters and digits becomes
// one '-', leading and trailing '-' go, and it is cut to 60 code points (then
// any trailing '-' goes again). When nothing is left it is 'message'.
export function fileName(room: string, at: Date): string {
  const slug = room
    .normalize('NFKC')
    .replace(/[^\p{L}\p{N}]+/gu, '-')
    .replace(/^-+|-+$/g, '');
  const cut = [...slug].slice(0, 60).join('').replace(/-+$/, '');
  const date = `${at.getFullYear()}${pad(at.getMonth() + 1)}${pad(at.getDate())}`;
  const time = `${pad(at.getHours())}${pad(at.getMinutes())}`;
  return `${cut === '' ? 'message' : cut}-${date}-${time}.md`;
}

// How long the object URL lives after the click. Some browsers (Safari among
// them) start the download after click() returns and fail it if the URL is
// already gone.
const REVOKE_AFTER_MS = 10_000;

// saveText hands the browser text as a file to save. The link is never added
// to the document. The URL is revoked REVOKE_AFTER_MS after the click, or at
// once when the click throws.
export function saveText(name: string, text: string): void {
  const body = text.endsWith('\n') ? text : `${text}\n`;
  const url = URL.createObjectURL(new Blob([body], { type: 'text/markdown;charset=utf-8' }));
  try {
    const a = document.createElement('a');
    a.href = url;
    a.download = name;
    a.click();
  } catch (err) {
    URL.revokeObjectURL(url);
    throw err;
  }
  setTimeout(() => URL.revokeObjectURL(url), REVOKE_AFTER_MS);
}
