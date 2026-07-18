<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { api } from '$lib/api';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Card from '$lib/components/ui/card';
	import * as Table from '$lib/components/ui/table';
	import CpuIcon from '@lucide/svelte/icons/cpu';
	import MemoryStickIcon from '@lucide/svelte/icons/memory-stick';
	import HardDriveIcon from '@lucide/svelte/icons/hard-drive';
	import ActivityIcon from '@lucide/svelte/icons/activity';

	type Stats = {
		cpu: number;
		mem: { used: number; total: number };
		disk: { used: number; total: number };
		net: { rx: number; tx: number };
		apps: { app: string; cpu: string; mem: string }[];
	};

	let s = $state<Stats | null>(null);
	let timer: ReturnType<typeof setInterval>;

	const gb = (n: number) => (n / 1073741824).toFixed(1) + ' GB';
	const rate = (n: number) => (n > 1048576 ? (n / 1048576).toFixed(1) + ' MB/s' : (n / 1024).toFixed(0) + ' KB/s');
	const pct = (used: number, total: number) => (total ? (100 * used) / total : 0);

	async function load() {
		try {
			s = await api('/stats');
		} catch {
			// transient — next poll retries
		}
	}
	onMount(() => {
		load();
		timer = setInterval(load, 4000);
	});
	onDestroy(() => clearInterval(timer));
</script>

{#snippet meter(label: string, value: string, percent: number, Icon: any)}
	<Card.Root>
		<Card.Header>
			<Card.Description class="flex items-center gap-2"><Icon class="size-4" /> {label}</Card.Description>
			<Card.Title class="text-xl tabular-nums">{value}</Card.Title>
		</Card.Header>
		<Card.Content>
			<div class="bg-secondary h-1.5 w-full overflow-hidden rounded-full">
				<div
					class="h-full rounded-full {percent > 85 ? 'bg-red-500' : 'bg-primary'}"
					style="width: {Math.min(100, percent)}%"
				></div>
			</div>
		</Card.Content>
	</Card.Root>
{/snippet}

<div class="mx-auto max-w-5xl">
	<h1 class="mb-6 text-2xl font-semibold tracking-tight">Overview</h1>

	{#if !s}
		<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
			{#each Array(4) as _, i (i)}<Skeleton class="h-32" />{/each}
		</div>
	{:else}
		<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
			{@render meter('CPU', s.cpu.toFixed(0) + '%', s.cpu, CpuIcon)}
			{@render meter('Memory', gb(s.mem.used) + ' / ' + gb(s.mem.total), pct(s.mem.used, s.mem.total), MemoryStickIcon)}
			{@render meter('Disk', gb(s.disk.used) + ' / ' + gb(s.disk.total), pct(s.disk.used, s.disk.total), HardDriveIcon)}
			<Card.Root>
				<Card.Header>
					<Card.Description class="flex items-center gap-2"><ActivityIcon class="size-4" /> Network</Card.Description>
					<Card.Title class="text-xl tabular-nums">↓ {rate(s.net.rx)}</Card.Title>
				</Card.Header>
				<Card.Content>
					<p class="text-muted-foreground text-sm tabular-nums">↑ {rate(s.net.tx)}</p>
				</Card.Content>
			</Card.Root>
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
						{#each s.apps as a (a.app)}
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
