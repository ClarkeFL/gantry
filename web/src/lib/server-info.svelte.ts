// Server identity shared across the app, filled in by the layout from /api/me.
// tzOffsetMin is minutes east of UTC (e.g. UTC = 0, AEST = 600).
export const serverInfo = $state({ tz: '', tzOffsetMin: 0, known: false });

export function browserOffsetMin(): number {
	return -new Date().getTimezoneOffset();
}

// Server wall-clock time right now, formatted HH:MM.
export function serverClock(): string {
	if (!serverInfo.known) return '';
	const shifted = new Date(Date.now() + (serverInfo.tzOffsetMin - browserOffsetMin()) * 60000);
	return shifted.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}
