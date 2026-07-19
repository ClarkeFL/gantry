<script lang="ts">
	// Friendly cron builder: pick a frequency and real times/days instead of
	// writing cron syntax. Custom mode still accepts raw 5-field cron.
	// Times are entered in the operator's timezone (Settings → Your timezone)
	// and converted to server clock for the stored cron expression.
	let {
		value = $bindable(''),
		allowEmpty = false,
		compact = false,
		onchange
	}: { value?: string; allowEmpty?: boolean; compact?: boolean; onchange?: () => void } = $props();

	import {
		serverInfo,
		displayPrefs,
		userOffsetMin,
		userTzLabel,
		serverTzLabel,
		shiftHM
	} from '$lib/server-info.svelte';

	const DAYS: [string, string][] = [
		['1', 'Monday'],
		['2', 'Tuesday'],
		['3', 'Wednesday'],
		['4', 'Thursday'],
		['5', 'Friday'],
		['6', 'Saturday'],
		['0', 'Sunday']
	];

	// starts as 'daily'; the value-parsing effect below switches to 'off'
	// for an empty value when allowEmpty is set
	let mode = $state('daily');
	let everyN = $state(15);
	let time = $state('03:00'); // always in the operator's timezone
	let weekday = $state('1');
	let monthday = $state(1);
	let custom = $state('');
	let lastEmitted: string | null = null;

	function pad(n: number) {
		return String(n).padStart(2, '0');
	}

	function hm(): [number, number] {
		const [h, m] = time.split(':').map((x) => parseInt(x, 10));
		return [isNaN(h) ? 0 : h, isNaN(m) ? 0 : m];
	}

	// User-entered time → server clock (what cron actually runs on).
	function toServerHM(): [number, number] {
		const [h, m] = hm();
		if (!serverInfo.known) return [h, m];
		return shiftHM(h, m, userOffsetMin(), serverInfo.tzOffsetMin);
	}

	// Server clock → user-entered time for the picker.
	function fromServerHM(h: number, m: number): [number, number] {
		if (!serverInfo.known) return [h, m];
		return shiftHM(h, m, serverInfo.tzOffsetMin, userOffsetMin());
	}

	function build(): string {
		const [h, m] = toServerHM();
		switch (mode) {
			case 'off':
				return '';
			case 'minutes':
				return `*/${everyN} * * * *`;
			case 'hourly':
				return '0 * * * *';
			case 'daily':
				return `${m} ${h} * * *`;
			case 'weekly':
				return `${m} ${h} * * ${weekday}`;
			case 'monthly':
				return `${m} ${h} ${monthday} * *`;
			default:
				return custom.trim();
		}
	}

	function emit() {
		if (mode === 'custom' && !custom.trim() && lastEmitted) custom = lastEmitted;
		lastEmitted = build();
		value = lastEmitted;
		onchange?.();
	}

	function parse(v: string) {
		v = (v ?? '').trim();
		let m: RegExpMatchArray | null;
		if (!v) {
			if (allowEmpty) mode = 'off';
			return;
		}
		if ((m = v.match(/^\*\/(\d+) \* \* \* \*$/))) {
			mode = 'minutes';
			everyN = +m[1];
		} else if (v === '0 * * * *') {
			mode = 'hourly';
		} else if ((m = v.match(/^(\d+) (\d+) \* \* \*$/))) {
			mode = 'daily';
			const [uh, um] = fromServerHM(+m[2], +m[1]);
			time = pad(uh) + ':' + pad(um);
		} else if ((m = v.match(/^(\d+) (\d+) \* \* ([0-6])$/))) {
			mode = 'weekly';
			const [uh, um] = fromServerHM(+m[2], +m[1]);
			time = pad(uh) + ':' + pad(um);
			weekday = m[3];
		} else if ((m = v.match(/^(\d+) (\d+) (\d+) \* \*$/))) {
			mode = 'monthly';
			const [uh, um] = fromServerHM(+m[2], +m[1]);
			time = pad(uh) + ':' + pad(um);
			monthday = +m[3];
		} else {
			mode = 'custom';
			custom = v;
		}
	}

	$effect(() => {
		if (value !== lastEmitted) {
			parse(value);
			lastEmitted = value;
		}
	});

	// When the operator's timezone preference (or server offset) changes,
	// force a re-parse of the stored server-time cron into the picker.
	let tzKey = $state('');
	$effect(() => {
		const next = displayPrefs.tz + ':' + serverInfo.tzOffsetMin;
		if (next === tzKey) return;
		tzKey = next;
		lastEmitted = null;
	});

	const hasTime = $derived(mode === 'daily' || mode === 'weekly' || mode === 'monthly');

	const serverTimeStr = $derived.by(() => {
		const [h, m] = toServerHM();
		return `${pad(h)}:${pad(m)}`;
	});

	const yourLabel = $derived(userTzLabel());
	const srvLabel = $derived(serverTzLabel() || 'server');
	const sameZone = $derived(
		serverInfo.known && userOffsetMin() === serverInfo.tzOffsetMin
	);

	const summary = $derived.by(() => {
		const [h, m] = hm();
		const t = pad(h) + ':' + pad(m);
		switch (mode) {
			case 'off':
				return 'Not scheduled';
			case 'minutes':
				return `Runs every ${everyN} minutes (server clock)`;
			case 'hourly':
				return 'Runs every hour, on the hour (server clock)';
			case 'daily':
				return sameZone
					? `Runs every day at ${t} ${yourLabel}`
					: `Runs every day at ${t} ${yourLabel} · ${serverTimeStr} ${srvLabel} on the server`;
			case 'weekly':
				return sameZone
					? `Runs every ${DAYS.find((d) => d[0] === weekday)?.[1]} at ${t} ${yourLabel}`
					: `Runs every ${DAYS.find((d) => d[0] === weekday)?.[1]} at ${t} ${yourLabel} · ${serverTimeStr} ${srvLabel} on the server`;
			case 'monthly':
				return sameZone
					? `Runs on day ${monthday} of every month at ${t} ${yourLabel}`
					: `Runs on day ${monthday} of every month at ${t} ${yourLabel} · ${serverTimeStr} ${srvLabel} on the server`;
			default:
				return custom.trim().split(/\s+/).length === 5
					? `Custom cron (server time${srvLabel ? `, ${srvLabel}` : ''})`
					: `Needs 5 parts: minute hour day month weekday (server time${srvLabel ? `, ${srvLabel}` : ''})`;
		}
	});

	const ctl =
		'border-input dark:bg-input/30 h-9 rounded-md border bg-transparent px-2.5 text-sm shadow-xs';
</script>

<div class="grid gap-1.5">
	<div class="flex flex-wrap items-center gap-2">
		<select class={ctl} bind:value={mode} onchange={emit} aria-label="How often">
			{#if allowEmpty}<option value="off">Off</option>{/if}
			<option value="minutes">Every few minutes</option>
			<option value="hourly">Every hour</option>
			<option value="daily">Every day</option>
			<option value="weekly">Every week</option>
			<option value="monthly">Every month</option>
			<option value="custom">Custom (cron)</option>
		</select>
		{#if mode === 'minutes'}
			<span class="text-muted-foreground text-sm">every</span>
			<select class={ctl} bind:value={everyN} onchange={emit} aria-label="Minutes">
				{#each [5, 10, 15, 30] as n (n)}<option value={n}>{n} min</option>{/each}
			</select>
		{:else if mode === 'weekly'}
			<span class="text-muted-foreground text-sm">on</span>
			<select class={ctl} bind:value={weekday} onchange={emit} aria-label="Day of week">
				{#each DAYS as [v, label] (v)}<option value={v}>{label}</option>{/each}
			</select>
		{:else if mode === 'monthly'}
			<span class="text-muted-foreground text-sm">on day</span>
			<input
				type="number"
				min="1"
				max="28"
				class="{ctl} w-16"
				bind:value={monthday}
				onchange={emit}
				aria-label="Day of month"
			/>
		{/if}
		{#if hasTime}
			<span class="text-muted-foreground text-sm">at</span>
			<input type="time" class={ctl} bind:value={time} onchange={emit} aria-label="Time in your timezone" />
			<span
				class="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-[11px] font-medium tracking-wide"
				title="Times are in your timezone (Settings → Your timezone). Cron runs on the server clock."
			>
				{yourLabel}
			</span>
		{:else if mode === 'custom'}
			<input
				class="{ctl} w-36 font-mono text-xs"
				placeholder="0 3 * * *"
				bind:value={custom}
				oninput={emit}
				aria-label="Cron expression (server time)"
			/>
			<span class="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-[11px] font-medium tracking-wide">
				{srvLabel || 'server'}
			</span>
		{/if}
	</div>
	<p class="text-muted-foreground text-xs">
		{summary}{#if !compact && mode !== 'off' && mode !== 'custom'}
			<span class="ml-1.5 font-mono opacity-60"> · {build()}</span>
		{/if}
	</p>
</div>
