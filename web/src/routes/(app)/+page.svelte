<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { api } from '$lib/api';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Card from '$lib/components/ui/card';
	import * as Table from '$lib/components/ui/table';

	type Point = { cpu: number; mem: number; disk: number; net: number };
	type Stats = {
		cpu: { pct: number; cores: number; load: string };
		mem: { used: number; total: number };
		disk: { used: number; total: number };
		net: number;
		hist: Point[];
		apps: { app: string; cpu: string; mem: string }[];
	};

	let s = $state<Stats | null>(null);
	let timer: ReturnType<typeof setInterval>;
	let tip = $state<{ x: number; y: number; text: string } | null>(null);

	const gb = (n: number) => (n / 1073741824).toFixed(1) + ' GB';
	const rate = (n: number) =>
		n >= 1048576 ? (n / 1048576).toFixed(1) + ' MB/s' : n >= 1024 ? (n / 1024).toFixed(0) + ' KB/s' : n.toFixed(0) + ' B/s';
	const pct = (used: number, total: number) => (total ? (100 * used) / total : 0);

	async function load() {
		try {
			s = await api('/stats');
		} catch {
			// transient, next poll retries
		}
	}
	onMount(() => {
		load();
		timer = setInterval(load, 5000);
	});
	onDestroy(() => clearInterval(timer));

	function path(values: number[], max: number): string {
		if (values.length < 2) return '';
		const n = values.length - 1;
		return values
			.map((v, i) => `${i === 0 ? 'M' : 'L'}${((i / n) * 200).toFixed(1)},${(38 - (Math.min(v, max) / max) * 34).toFixed(1)}`)
			.join(' ');
	}

	function hover(e: PointerEvent, values: number[], fmt: (v: number) => string) {
		const svg = e.currentTarget as SVGSVGElement;
		const r = svg.getBoundingClientRect();
		const i = Math.round(((e.clientX - r.left) / r.width) * (values.length - 1));
		if (i < 0 || i >= values.length) return;
		const ago = (values.length - 1 - i) * 5;
		tip = {
			x: e.clientX,
			y: r.top,
			text: `${fmt(values[i])} · ${ago === 0 ? 'now' : ago < 60 ? ago + 's ago' : Math.round(ago / 60) + 'm ago'}`
		};
	}
</script>

{#snippet tile(label: string, value: string, caption: string, values: number[], max: number, color: string, fmt: (v: number) => string)}
	<Card.Root class="gap-3">
		<Card.Header>
			<Card.Description>{label}</Card.Description>
			<Card.Title class="text-2xl tabular-nums">{value}</Card.Title>
			<Card.Description class="text-xs">{caption}</Card.Description>
		</Card.Header>
		<Card.Content>
			<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
			<svg
				viewBox="0 0 200 40"
				preserveAspectRatio="none"
				class="h-10 w-full cursor-crosshair"
				role="img"
				aria-label="{label} over the last 10 minutes"
				onpointermove={(e) => hover(e, values, fmt)}
				onpointerleave={() => (tip = null)}
			>
				<path d={path(values, max)} fill="none" stroke={color} stroke-width="1.5" vector-effect="non-scaling-stroke" />
			</svg>
		</Card.Content>
	</Card.Root>
{/snippet}

<div class="mx-auto max-w-5xl">
	<h1 class="mb-6 text-2xl font-semibold tracking-tight">Overview</h1>

	{#if !s}
		<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
			{#each Array(4) as _, i (i)}<Skeleton class="h-36" />{/each}
		</div>
	{:else}
		{@const netMax = Math.max(1024, ...s.hist.map((p) => p.net))}
		<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
			{@render tile(
				'CPU',
				s.cpu.pct.toFixed(1) + '%',
				`${s.cpu.cores} cores, Load ${s.cpu.load}`,
				s.hist.map((p) => p.cpu),
				100,
				'#d95926',
				(v) => v.toFixed(1) + '%'
			)}
			{@render tile(
				'Memory',
				pct(s.mem.used, s.mem.total).toFixed(1) + '%',
				`${gb(s.mem.used)} / ${gb(s.mem.total)}`,
				s.hist.map((p) => p.mem),
				100,
				'#3987e5',
				(v) => v.toFixed(1) + '%'
			)}
			{@render tile(
				'Disk',
				pct(s.disk.used, s.disk.total).toFixed(1) + '%',
				`${gb(s.disk.used)} / ${gb(s.disk.total)}`,
				s.hist.map((p) => p.disk),
				100,
				'#008300',
				(v) => v.toFixed(1) + '%'
			)}
			{@render tile(
				'Network',
				rate(s.net),
				'rx + tx, last 10 min',
				s.hist.map((p) => p.net),
				netMax,
				'#9085e9',
				rate
			)}
		</div>

		<h2 class="text-muted-foreground mt-10 mb-2 text-xs font-medium tracking-widest uppercase">Per-app usage</h2>
		{#if s.apps.length}
			<div class="rounded-lg border">
				<Table.Root>
					<Table.Header>
						<Table.Row>
							<Table.Head>App</Table.Head>
							<Table.Head>CPU</Table.Head>
							<Table.Head>Memory</Table.Head>
						</Table.Row>
					</Table.Header>
					<Table.Body>
						{#each s.apps as a, i (a.app + i)}
							<Table.Row>
								<Table.Cell><a class="hover:underline" href="/app/{a.app}">{a.app}</a></Table.Cell>
								<Table.Cell class="font-mono text-xs tabular-nums">{a.cpu}</Table.Cell>
								<Table.Cell class="font-mono text-xs tabular-nums">{a.mem}</Table.Cell>
							</Table.Row>
						{/each}
					</Table.Body>
				</Table.Root>
			</div>
		{:else}
			<p class="text-muted-foreground text-sm">No running containers.</p>
		{/if}
	{/if}
</div>

{#if tip}
	<div
		class="bg-popover text-popover-foreground pointer-events-none fixed z-50 -translate-x-1/2 -translate-y-full rounded-md border px-2 py-1 font-mono text-xs whitespace-nowrap shadow-md"
		style="left: {tip.x}px; top: {tip.y - 6}px"
	>
		{tip.text}
	</div>
{/if}
