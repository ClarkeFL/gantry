export async function api<T = any>(path: string, opts: RequestInit = {}): Promise<T> {
	const res = await fetch('/api' + path, {
		headers: { 'Content-Type': 'application/json' },
		...opts
	});
	if (res.status === 401 && path !== '/login' && location.pathname !== '/login') {
		location.href = '/login';
		throw new Error('unauthorized');
	}
	if (!res.ok) {
		const body = await res.json().catch(() => ({}) as any);
		throw new Error(body.error || res.statusText || `request failed (HTTP ${res.status})`);
	}
	return res.json();
}

// Reads an SSE response line-by-line; returns an abort function.
export function stream(
	path: string,
	onLine: (line: string) => void,
	opts: RequestInit = {},
	onDone?: () => void
) {
	const ctrl = new AbortController();
	fetch('/api' + path, { headers: { 'Content-Type': 'application/json' }, ...opts, signal: ctrl.signal })
		.then(async (res) => {
			if (!res.ok || !res.body) {
				const body = await res.json().catch(() => ({}) as any);
				onLine('[error] ' + (body.error ?? res.statusText));
				return;
			}
			const reader = res.body.getReader();
			const dec = new TextDecoder();
			let buf = '';
			for (;;) {
				const { done, value } = await reader.read();
				if (done) break;
				buf += dec.decode(value, { stream: true });
				const parts = buf.split('\n\n');
				buf = parts.pop()!;
				for (const p of parts) if (p.startsWith('data: ')) onLine(p.slice(6));
			}
			onDone?.();
		})
		.catch(() => onDone?.());
	return () => ctrl.abort();
}
