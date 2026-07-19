// Human-readable dates for the whole app. Input is whatever the backend
// emits (RFC3339, date-only, or a line starting with a timestamp); output is
// the user's locale, e.g. "19 Jul 2026, 13:14" or "3 min ago".

export function fmtDate(iso: string): string {
	const d = new Date(iso);
	if (isNaN(d.getTime())) return iso;
	const opts: Intl.DateTimeFormatOptions = { day: 'numeric', month: 'short', year: 'numeric' };
	if (iso.includes('T')) {
		opts.hour = '2-digit';
		opts.minute = '2-digit';
	}
	return d.toLocaleString(undefined, opts);
}

export function ago(iso: string): string {
	const d = new Date(iso);
	if (isNaN(d.getTime())) return iso;
	const s = Math.max(0, (Date.now() - d.getTime()) / 1000);
	if (s < 60) return 'just now';
	if (s < 3600) return `${Math.floor(s / 60)} min ago`;
	if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
	if (s < 7 * 86400) return `${Math.floor(s / 86400)} d ago`;
	return fmtDate(iso);
}

// Formats the leading timestamp of a log line, keeping the rest untouched.
export function fmtLogLine(line: string): string {
	const i = line.search(/[\s\t]/);
	if (i < 1) return line;
	const ts = line.slice(0, i);
	if (isNaN(new Date(ts).getTime())) return line;
	return fmtDate(ts) + line.slice(i);
}
