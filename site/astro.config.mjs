// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import mermaid from 'astro-mermaid';

export default defineConfig({
  site: 'https://ghchinoy.github.io',
  base: '/moonshine-go',
  integrations: [
    starlight({
      title: 'moonshine-go',
      description: 'Pure-Go bindings for Moonshine on-device STT, streaming TTS, text embeddings, and voice agent cascade.',
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/ghchinoy/moonshine-go',
        },
      ],
      sidebar: [
        {
          label: 'Getting Started',
          items: [
            { label: 'Overview', slug: 'index' },
            { label: 'Hardware Acceleration', slug: 'guides/hardware-acceleration' },
            { label: 'Bundling libmoonshine', slug: 'guides/bundling-libmoonshine' },
            { label: 'FAQ', slug: 'guides/faq' },
          ],
        },
        {
          label: 'Guides',
          items: [
            { label: 'User Guide & CLI', slug: 'guides/user-guide' },
            { label: 'Hosting & Deployment', slug: 'guides/hosting' },
            { label: 'Testing with Docker', slug: 'guides/testing-with-container' },
          ],
        },
        {
          label: 'Samples & Voice Agent',
          items: [
            { label: 'Samples Catalog', slug: 'samples' },
            { label: 'Architectural Pattern Guide', slug: 'samples/guide' },
            { label: 'AgentFlow Tutorial', slug: 'samples/tutorial' },
            {
              label: 'Tier 0: Subscriptions',
              collapsed: true,
              items: [
                { label: 'go-listen (Go WS)', slug: 'samples/go-listen' },
                { label: 'grpc-listen (Go gRPC)', slug: 'samples/grpc-listen' },
                { label: 'python-listen (Python WS)', slug: 'samples/python-listen' },
                { label: 'go-stream-audio (PCM streaming)', slug: 'samples/go-stream-audio' },
              ],
            },
            {
              label: 'Tier 1: External Agents',
              collapsed: true,
              items: [
                { label: 'browser-listen (Web Audio mic)', slug: 'samples/browser-listen' },
                { label: 'browser-cascade-faq (Web voice agent)', slug: 'samples/browser-cascade-faq' },
                { label: 'go-cascade-faq (Go AgentFlow agent)', slug: 'samples/go-cascade-faq' },
                { label: 'python-agent (Python command agent)', slug: 'samples/python-agent' },
              ],
            },
            {
              label: 'Tier 2 & Native In-Process',
              collapsed: true,
              items: [
                { label: 'go-domain-customization', slug: 'samples/go-domain-customization' },
                { label: 'go-bulk-analysis', slug: 'samples/go-bulk-analysis' },
                { label: 'go-embedded (in-process STT & TTS)', slug: 'samples/go-embedded' },
                { label: 'mcp-transcribe (MCP tool server)', slug: 'samples/mcp-transcribe' },
                { label: 'desktop-app (Wails v2 GUI)', slug: 'samples/desktop-app' },
              ],
            },
          ],
        },
        {
          label: 'Benchmarks & Latency',
          slug: 'benchmarks',
        },
        {
          label: 'Changelog',
          slug: 'changelog',
        },
        {
          label: 'API Reference (GoDoc)',
          link: 'https://pkg.go.dev/github.com/ghchinoy/moonshine-go',
          attrs: { target: '_blank', rel: 'noopener noreferrer' },
        },
      ],
      customCss: ['./src/styles/custom.css'],
    }),
    mermaid(),
  ],
});
