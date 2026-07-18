<script lang="ts">
	import './layout.css';
	import { Toaster } from '$lib/components/ui/sonner';
	import favicon from '$lib/assets/favicon.svg';

	let { children } = $props();
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<title>gantry</title>
</svelte:head>

<Toaster richColors position="top-right" />
{@render children()}
