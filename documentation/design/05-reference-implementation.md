# Reference Shell Implementation

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

This file provides a single, complete reference implementation of the application shell defined in [`03-application-shell.md`](./03-application-shell.md), using the tokens from [`02-design-tokens.md`](./02-design-tokens.md) and the logo/slogan rules from [`01-brand-identity.md`](./01-brand-identity.md).

*(informative)* This snippet is a structural and aesthetic reference in React + Tailwind CSS, not a mandated implementation framework. Adapt the state management, hooks, and element tags to whatever the host framework expects — Next.js, Vue, Nuxt, and SvelteKit are all acceptable so long as the resulting DOM, class names, and behavior match what is shown here.

```tsx
import React, { useState } from 'react';
import {
  Activity, BookOpen, Folder, MessageSquare, Moon, Search, Settings,
  Sparkles, Sun, Tv,
} from 'lucide-react';

const MODULES = [
  { id: 'chat', icon: MessageSquare, label: 'Chat' },
  { id: 'stream', icon: Tv, label: 'Stream' },
  { id: 'books', icon: BookOpen, label: 'Books' },
  { id: 'files', icon: Folder, label: 'Files' },
] as const;

export default function App() {
  const [isDarkMode, setIsDarkMode] = useState(true);
  const [activeModule, setActiveModule] = useState<'chat' | 'stream' | 'books' | 'files'>('chat');

  return (
    <div
      className={`flex min-h-screen flex-col font-sans transition-colors duration-300 ${
        isDarkMode ? 'bg-[#0B0D13] text-[#F8FAFC]' : 'bg-[#F1F5F9] text-[#111827]'
      }`}
    >
      {/* 1. Global Header */}
      <header
        className={`z-20 flex h-14 items-center justify-between border-b px-6 transition-colors ${
          isDarkMode ? 'border-slate-800 bg-[#121620]' : 'border-slate-200 bg-white shadow-sm'
        }`}
      >
        <div className="flex w-48 items-center gap-2">
          {/* Ouroboros mark + HTML wordmark (see design/01-brand-identity.md) */}
          <svg viewBox="0 0 100 100" className="h-10 w-10" role="img" aria-label="Telos ouroboros mark">
            <path d="M 33 14 A 40 40 0 1 1 12 40" fill="none" stroke="currentColor"
                  strokeWidth="4.5" strokeLinecap="round" />
            <path d="M 34 14 C 26 10, 18 16, 21 24 C 24 29, 32 27, 36 22 C 39 18, 38 15, 34 14 Z"
                  fill="currentColor" />
            <circle cx="26" cy="19" r="1.5" fill={isDarkMode ? '#0B0D13' : '#FFFFFF'} />
          </svg>
          <span className="text-lg font-semibold tracking-wide">Telos</span>
        </div>

        {/* Global search */}
        <div className="mx-8 hidden max-w-xl flex-1 md:flex">
          <div className="group relative w-full">
            <Search className="absolute left-4 top-2 h-4 w-4 text-slate-400 transition-colors group-focus-within:text-sky-500" />
            <input
              type="text"
              placeholder="Title, Author, Series, Genre, or Tags..."
              className={`w-full rounded-lg border py-1.5 pl-11 pr-4 text-sm transition-all focus:outline-none focus-visible:ring-2 focus-visible:ring-sky-500 ${
                isDarkMode
                  ? 'border-slate-700 bg-[#1A1E29] text-[#F8FAFC] placeholder-slate-500 focus:border-sky-500/50'
                  : 'border-slate-300 bg-slate-100 text-[#111827] placeholder-slate-500 focus:border-sky-400 focus:bg-white'
              }`}
            />
          </div>
        </div>

        {/* Quick actions */}
        <div className="flex w-48 items-center justify-end gap-3">
          <button aria-label="Activity" className="text-slate-400 hover:text-slate-200"><Activity className="h-4 w-4" /></button>
          <button aria-label="AI features" className="text-slate-400 hover:text-sky-400"><Sparkles className="h-4 w-4" /></button>
          <button aria-label="Settings" className="text-slate-400 hover:text-slate-200"><Settings className="h-4 w-4" /></button>
          <button aria-label="Toggle theme" onClick={() => setIsDarkMode(!isDarkMode)} className="text-slate-400 hover:text-slate-200">
            {isDarkMode ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </button>
        </div>
      </header>

      <div className="flex flex-1 overflow-hidden">
        {/* 2. Global Nav Strip */}
        <nav
          className={`flex w-[72px] flex-shrink-0 flex-col items-center justify-between border-r py-4 transition-colors ${
            isDarkMode ? 'border-slate-800 bg-[#10131C]' : 'border-slate-200 bg-[#F8FAFC]'
          }`}
        >
          <div className="flex flex-col gap-2">
            {MODULES.map(({ id, icon: Icon, label }) => (
              <button
                key={id}
                aria-label={label}
                onClick={() => setActiveModule(id)}
                className={`rounded-xl p-3 transition-colors ${
                  activeModule === id
                    ? 'bg-sky-500/10 text-sky-400'
                    : 'text-slate-400 hover:bg-slate-800/50 hover:text-slate-200'
                }`}
              >
                <Icon className="h-5 w-5" />
              </button>
            ))}
          </div>
          <button
            aria-label="Profile: AD"
            className="flex h-10 w-10 items-center justify-center rounded-full bg-slate-800 text-xs font-semibold text-slate-200"
          >
            AD
          </button>
        </nav>

        {/* 3. Contextual Sidebar */}
        <aside
          className={`hidden w-64 flex-shrink-0 flex-col border-r transition-colors lg:flex ${
            isDarkMode ? 'border-slate-800 bg-[#151924]' : 'border-slate-200 bg-white'
          }`}
        >
          <div className="flex-1 overflow-y-auto py-3">{/* module sub-navigation */}</div>
        </aside>

        {/* 4. Central Arena */}
        <main className="flex flex-1 flex-col overflow-hidden">
          {/* active module renders here */}
        </main>

        {/* 5. Server SLA Sidebar */}
        <aside
          className={`hidden w-64 flex-shrink-0 flex-col border-l transition-colors xl:flex ${
            isDarkMode ? 'border-slate-800 bg-[#121621]' : 'border-slate-200 bg-white'
          }`}
        >
          {/* server specs, slogans, peers */}
        </aside>
      </div>
    </div>
  );
}
```

The logo is composed as an SVG mark plus an HTML text sibling, per the logo composition rule in [`01-brand-identity.md`](./01-brand-identity.md#logo-composition-rule). The Contextual Sidebar and Server SLA Sidebar visibility (`lg:flex`, `xl:flex`) follow the responsive tiers defined in [`03-application-shell.md` §7](./03-application-shell.md#7-responsive-behavior).

## Corrections from the original mockup *(informative)*

cyan accent classes replaced with sky equivalents; invalid z-index utility replaced with `z-20`; SVG text-element wordmark replaced with HTML sibling; icon-only buttons gained `aria-label`; contextual sidebar hidden below `lg` per responsive spec.
