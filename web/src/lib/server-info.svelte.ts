// Server identity shared across the app, filled in by the layout from /api/me.
// tzOffsetMin is minutes east of UTC (e.g. UTC = 0, AEST = 600).
export const serverInfo = $state({ tz: '', tzOffsetMin: 0, known: false });

// Operator-preferred IANA zone for schedule UIs. Empty = use the browser zone.
// Loaded from /api/settings (displayTz) in the app layout.
export const displayPrefs = $state({ tz: '' as string });

export function browserOffsetMin(): number {
	return -new Date().getTimezoneOffset();
}

export function browserTzName(): string {
	try {
		return Intl.DateTimeFormat().resolvedOptions().timeZone || 'local';
	} catch {
		return 'local';
	}
}

/** Active display timezone: saved preference, else browser. */
export function userTzName(): string {
	return displayPrefs.tz || browserTzName();
}

/** Minutes east of UTC for an IANA zone at a given instant (handles DST). */
export function offsetMinForTz(iana: string, at: Date = new Date()): number {
	if (!iana) return browserOffsetMin();
	try {
		const parts = new Intl.DateTimeFormat('en-US', {
			timeZone: iana,
			timeZoneName: 'longOffset',
			hour: 'numeric'
		}).formatToParts(at);
		const raw = parts.find((p) => p.type === 'timeZoneName')?.value ?? 'GMT';
		// GMT, GMT+10, GMT+10:30, GMT-5:00
		const m = raw.match(/GMT([+-])(\d{1,2})(?::(\d{2}))?/);
		if (!m) return 0;
		const sign = m[1] === '-' ? -1 : 1;
		return sign * (parseInt(m[2], 10) * 60 + parseInt(m[3] || '0', 10));
	} catch {
		return browserOffsetMin();
	}
}

export function userOffsetMin(at: Date = new Date()): number {
	return displayPrefs.tz ? offsetMinForTz(displayPrefs.tz, at) : browserOffsetMin();
}

function isGenericOffsetLabel(name: string): boolean {
	// Browsers often emit "GMT+10" / "UTC+10" instead of regional names like AEST.
	return /^(GMT|UTC)([+-]\d|$)/i.test(name.trim());
}

/** Short label for a zone, e.g. "AEST". Avoids ugly GMT+10 when possible. */
export function shortTzLabel(iana: string, at: Date = new Date()): string {
	if (!iana) return '';
	// en-AU/GB tend to return AEST/AEDT; en-US often returns GMT+10 for AU zones.
	for (const locale of ['en-AU', 'en-GB', 'en-US']) {
		try {
			const parts = new Intl.DateTimeFormat(locale, {
				timeZone: iana,
				timeZoneName: 'short'
			}).formatToParts(at);
			const name = parts.find((p) => p.type === 'timeZoneName')?.value;
			if (name && !isGenericOffsetLabel(name)) return name;
		} catch {
			/* try next locale */
		}
	}
	// Same offset as the server: reuse its zone name (Go gives AEST, not GMT+10).
	try {
		if (serverInfo.known && serverInfo.tz && offsetMinForTz(iana, at) === serverInfo.tzOffsetMin) {
			return serverInfo.tz;
		}
	} catch {
		/* ignore */
	}
	// Known AU / common aliases when Intl only has an offset.
	const city = iana.split('/').pop()?.replace(/_/g, ' ') ?? '';
	const known: Record<string, string> = {
		'Australia/Brisbane': 'AEST',
		'Australia/Sydney': shortSeasonalAu(at, 'AEDT', 'AEST'),
		'Australia/Melbourne': shortSeasonalAu(at, 'AEDT', 'AEST'),
		'Australia/Hobart': shortSeasonalAu(at, 'AEDT', 'AEST'),
		'Australia/Adelaide': shortSeasonalAu(at, 'ACDT', 'ACST'),
		'Australia/Perth': 'AWST',
		'Australia/Darwin': 'ACST',
		UTC: 'UTC',
		'Etc/UTC': 'UTC'
	};
	if (known[iana]) return known[iana];
	return city || iana;
}

// Rough southern-hemisphere DST window (first Sun Oct → first Sun Apr).
function shortSeasonalAu(at: Date, summer: string, winter: string): string {
	const m = at.getUTCMonth(); // 0-11
	// Oct–Mar ≈ DST for most of AU east coast (good enough for a label).
	return m >= 9 || m <= 2 ? summer : winter;
}

/**
 * Label for the operator's timezone.
 * Prefers a short regional name (AEST), else the IANA zone (Australia/Brisbane).
 * Never returns GMT+10-style offsets.
 */
export function userTzLabel(at: Date = new Date()): string {
	const iana = userTzName();
	if (!iana) return 'local';
	const short = shortTzLabel(iana, at);
	if (short && !isGenericOffsetLabel(short)) return short;
	return iana;
}

/** Longer label: "AEST (Australia/Brisbane)" when both are useful. */
export function userTzFull(at: Date = new Date()): string {
	const iana = userTzName();
	if (!iana) return 'local';
	const short = shortTzLabel(iana, at);
	if (short && !isGenericOffsetLabel(short) && short !== iana) {
		return `${short} · ${iana}`;
	}
	return iana;
}

export function serverTzLabel(): string {
	// Prefer Go's zone name (AEST). Fall back to IANA if we only had an offset.
	if (serverInfo.tz && !isGenericOffsetLabel(serverInfo.tz)) return serverInfo.tz;
	return serverInfo.known ? 'server' : '';
}

// Server wall-clock time right now, formatted HH:MM.
export function serverClock(): string {
	if (!serverInfo.known) return '';
	const shifted = new Date(Date.now() + (serverInfo.tzOffsetMin - browserOffsetMin()) * 60000);
	return shifted.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

/** Shift HH:MM from one offset to another (both minutes east of UTC). */
export function shiftHM(
	h: number,
	m: number,
	fromOffsetMin: number,
	toOffsetMin: number
): [number, number] {
	const total = (((h * 60 + m - (fromOffsetMin - toOffsetMin)) % 1440) + 1440) % 1440;
	return [Math.floor(total / 60), total % 60];
}
