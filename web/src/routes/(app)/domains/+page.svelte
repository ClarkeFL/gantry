<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Table from '$lib/components/ui/table';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';

	type Row = { domain: string; app: string };
	let rows = $state<Row[]>([]);
	let loading = $state(true);

	onMount(async () => {
		try {
			rows = (await api('/domains')).domains ?? [];
		} finally {
			loading = false;
		}
	});
</script>

<div class="mx-auto max-w-4xl">
	<h1 class="mb-6 text-2xl font-semibold tracking-tight">Domains</h1>
	{#if loading}
		<Skeleton class="h-40" />
	{:else if rows.length}
		<div class="rounded-lg border">
			<Table.Root>
				<Table.Header>
					<Table.Row>
						<Table.Head>Domain</Table.Head>
						<Table.Head>App</Table.Head>
						<Table.Head class="w-10"></Table.Head>
					</Table.Row>
				</Table.Header>
				<Table.Body>
					{#each rows as r (r.domain + r.app)}
						<Table.Row>
							<Table.Cell class="font-mono text-xs">{r.domain}</Table.Cell>
							<Table.Cell><a class="hover:underline" href="/app/{r.app}">{r.app}</a></Table.Cell>
							<Table.Cell>
								<a href="https://{r.domain}" target="_blank" rel="noreferrer" aria-label="Open {r.domain}">
									<ExternalLinkIcon class="text-muted-foreground hover:text-foreground size-4" />
								</a>
							</Table.Cell>
						</Table.Row>
					{/each}
				</Table.Body>
			</Table.Root>
		</div>
	{:else}
		<p class="text-muted-foreground text-sm">
			No domains configured yet. Add one with <code>dokku domains:add &lt;app&gt; &lt;domain&gt;</code> —
			in-panel domain management is coming.
		</p>
	{/if}
</div>
