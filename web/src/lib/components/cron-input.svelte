<script lang="ts">
	// Friendly cron builder: pick a frequency and real times/days instead of
	// writing cron syntax. Custom mode still accepts raw 5-field cron. Always
	// shows the generated schedule and a plain-English summary.
	let {
		value = $bindable(''),
		allowEmpty = false,
		compact = false,
		onchange
	}: { value?: string; allowEmpty?: boolean; compact?: boolean; onchange?: () => void } = $props();

	import { serverInfo, browserOffsetMin } from '$lib/server-info.svelte';

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
	let time = $state('03:00');
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

	function build(): string {
		const [h, m] = hm();
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
			time = pad(+m[2]) + ':' + pad(+m[1]);
		} else if ((m = v.match(/^(\d+) (\d+) \* \* ([0-6])$/))) {
			mode = 'weekly';
			time = pad(+m[2]) + ':' + pad(+m[1]);
			weekday = m[3];
		} else if ((m = v.match(/^(\d+) (\d+) (\d+) \* \*$/))) {
			mode = 'monthly';
			time = pad(+m[2]) + ':' + pad(+m[1]);
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

	// schedules run on the server's clock; when the viewer is in a different
	// timezone, show their local equivalent next to the picked time
	const tzHint = $derived.by(() => {
		if (!serverInfo.known) return '';
		const diff = browserOffsetMin() - serverInfo.tzOffsetMin;
		if (diff === 0) return '';
		const [h, m] = hm();
		const total = (h * 60 + m + diff + 2880) % 1440;
		return ` server time (${pad(Math.floor(total / 60))}:${pad(total % 60)} your time)`;
	});

	const summary = $derived.by(() => {
		const [h, m] = hm();
		const t = pad(h) + ':' + pad(m);
		switch (mode) {
			case 'off':
				return 'Not scheduled';
			case 'minutes':
				return `Runs every ${everyN} minutes`;
			case 'hourly':
				return 'Runs every hour, on the hour';
			case 'daily':
				return `Runs every day at ${t}${tzHint}`;
			case 'weekly':
				return `Runs every ${DAYS.find((d) => d[0] === weekday)?.[1]} at ${t}${tzHint}`;
			case 'monthly':
				return `Runs on day ${monthday} of every month at ${t}${tzHint}`;
			default:
				return custom.trim().split(/\s+/).length === 5
					? 'Custom schedule'
					: 'Needs 5 parts: minute hour day month weekday';
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
		{#if mode === 'daily' || mode === 'weekly' || mode === 'monthly'}
			<span class="text-muted-foreground text-sm">at</span>
			<input type="time" class={ctl} bind:value={time} onchange={emit} aria-label="Time" />
		{:else if mode === 'custom'}
			<input
				class="{ctl} w-32 font-mono text-xs"
				placeholder="0 3 * * *"
				bind:value={custom}
				oninput={emit}
				aria-label="Cron expression"
			/>
		{/if}
	</div>
	<p class="text-muted-foreground text-xs">
		{summary}{#if !compact && mode !== 'off' && mode !== 'custom'}<span class="ml-1.5 font-mono opacity-60">{build()}</span>{/if}
	</p>
</div>
