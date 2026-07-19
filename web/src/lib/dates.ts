// Human-readable dates for the whole app. Input is whatever the backend
// emits (RFC3339, date-only, or a line starting with a timestamp); output is
// the operator's preferred timezone (Settings → Your timezone), e.g.
// "19 Jul 2026, 1:14 pm AEST".

import { userTzName, userTzLabel } from '$lib/server-info.svelte';

export function fmtDate(iso: string): string {
	const d = new Date(iso);
	if (isNaN(d.getTime())) return iso;
	const tz = userTzName() || undefined;
	const hasTime = /T|\d:\d/.test(iso);
	const opts: Intl.DateTimeFormatOptions = {
		day: 'numeric',
		month: 'short',
		year: 'numeric',
		timeZone: tz
	};
	if (hasTime) {
		opts.hour = '2-digit';
		opts.minute = '2-digit';
	}
	const base = d.toLocaleString(undefined, opts);
	if (!hasTime) return base;
	const label = userTzLabel();
	return label ? `${base} ${label}` : base;
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
