Telos - Frontend UI/UX Design SpecificationTarget Audience: AI Coding Agents (Claude, Cursor, Auto-GPT)Project Context: Telos is an open-source, self-hosted application merging Discord-style chat/voice, Jellyfin-style media streaming, Booklore-style resource (eBook) management, and a built-in file management system into a single unified frontend.1. Brand Identity & PhilosophyCore Philosophy: Telos is an infrastructure of digital sovereignty. It is a self-hosted, uncompromised digital home in an increasingly commercialized world. The visual identity must feel like a "premium utility"—ancient utility meets sovereign futurism.Slogans:"your server, your community" (Community-facing)"be on the net, but not of the net" (Philosophical/Technical)Logo Element:Name: The Sovereign Ouroboros.Description: A minimalist, continuous loop representing a serpent eating its own tail. Inside the loop sits the typography for "Telos".Implementation: Must be rendered as a crisp, inline SVG using standard <path> and <circle> elements, entirely monochromatic (scaling with the current text color).2. Global Design TokensColor Palette (Strictly Enforced)CRITICAL INSTRUCTION: Do NOT use multi-colored semantic accents (no red, green, yellow, amber, or emerald). The entire application must rely exclusively on a grayscale neutral palette paired with stark white and a singular defining Blue accent.Backgrounds (Dark Mode Default):App Background: Deep Void (#0B0D13 or slate-950)Panel/Card Background: Dark Slate (#121620 or slate-900)Hover States: Elevated Slate (slate-800 or slate-800/50)Text & Typography:Primary Text: Stark White (#F8FAFC or slate-50)Secondary Text: Muted Grey (slate-400 or slate-500)Brand/Accent (The "Telos Blue"):Primary Accent: Sovereign Blue (#0ea5e9 - Tailwind sky-500 or cyan-500). Used sparingly for active states, primary buttons, AI oracle features, and progress bars.Muted Accent: Blue Wash (sky-500/10 with sky-400 text). Used for badges and active voice channel states.TypographyPrimary (UI/Headers/Body): Inter, Geist Sans, or system-ui. Clean, humanist sans-serif.Secondary (Metrics/Logs/Code/System): JetBrains Mono or Fira Code. Used for all server metrics, file paths, and system logs.Iconography & UI ElementsIcons: Use lucide-react exclusively.Rule: NO EMOJIS. All icons must be sleek, monochromatic SVG outlines.Borders & Radii: Use clean borders (border-slate-800 in dark mode). Border radii should be md (8px) for inputs/buttons and xl or 2xl for large structural cards.3. Global Layout ArchitectureThe application uses a 4-pane horizontal flex layout to prevent context switching between apps.Global Header (Top Bar):Left: Telos Ouroboros SVG Logo.Center: Global Search Bar (Placeholder: "Title, Author, Series, Genre, or Tags..."). Focus ring uses Telos Blue.Right: Quick action icons (Activity, Sparkles, Settings), Voice status indicator, Theme toggle (Sun/Moon), and a "SOVEREIGNTY MANIFESTO" toggle button.Global Navigation (Left-Most Strip):Vertical icon list to switch activeModule.Modules: Chat (MessageSquare), Stream (Tv), Books (BookOpen), Files (Folder).Bottom: User Avatar profile circle (monogram "AD" for admin), hovering reveals local node details.Contextual Sidebar (Inner Left):Dynamic content based on the active module.Footer: Displays active local nodes/peers and encrypted tunnel status.Central Arena (Main Canvas):The high-performance workspace where the active content is displayed (Chat logs, Media Player, Book Reader, File lists).Server SLA Sidebar (Right-Most, Hidden on small screens):Displays "Sovereign Node SLA".Contains Server Specifications (Host OS, DB Engine).Prominently displays the two brand Slogans in stylized UI boxes.Lists active connected peers.4. Module SpecificationsA. Chat & VoiceSidebar: Lists text channels (prepended with Hash icon) and Voice Channels (with Join/Leave states and nested active user lists).Main Arena: * Header contains an AI "SUMMARIZE MISSED" button (uses lucide-react Bot icon and Telos Blue accent).Brand banner emphasizing the "be on the net, but not of the net" philosophy.Chat messages with fully rounded monograms, username, role badge (Host/Admin), and timestamp.Bottom integrated input field.B. Media Streaming (Jellyfin Style)Sidebar: Media Libraries (Movies, Documentaries, Audiobooks) and an "Ingest Stream" URL tool.Main Arena: * Large, central HTML5-style video player canvas. Includes play/pause, timeline, duration, and direct-stream bitrate badges.Below the player: Grid of active media feeds/thumbnails with blue progress bars attached directly to the bottom of the thumbnail covers.C. Booklore (eBook Management)Sidebar: Matches the classic Booklore UI. Sections for "Home" (Dashboard, All Books, Authors), "Libraries", and "Shelves", complete with numerical count badges aligned to the right.Main Arena (Library View):"Continue Listening" and "Continue Reading" horizontal sections. Headers feature a thick Telos Blue underline accent.Book cards: Format badge (EPUB/PDF) in top left. Title and Author centered on a neutral dark cover. Play icon overlay on hover.Main Arena (Reader View):Clean, serif-font reading experience.Font-size toggle controls.Includes an AI "ANALYZE CHAPTER" button in the footer to generate LLM-powered philosophical summaries of the current text.D. File System ManagerSidebar: Storage Volume metrics (Progress bars showing disk usage for /mnt/ssd_nvme etc.) and Quick Navigation links.Main Arena: * Top bar: Breadcrumb navigation and "New Directory" / "Upload" buttons.Grid-list view of files displaying Name, Size, and Modified Date.Selection state opens a contextual bottom action bar for Rename, Delete, and Download.E. Sovereignty Manifesto (System View)Main Arena: * Accessed via the header button.Explains "The Digital Enclosure Problem" and "The Telos Design Creed".Features a mocked-up docker-compose.yml terminal block highlighting local loopback configs and encryption keys.5. Reference Code Implementation (Structural Guide)⚠️ ARCHITECTURE DISCLAIMER:The following code snippet represents the interactive mockup built in standard React (JSX). The final application may utilize a different frontend framework (e.g., Next.js, Vue, Nuxt, or SvelteKit).This snippet is provided as a structural, state-management, and aesthetic reference (Tailwind classes, SVG composition, flex layouts). Do not treat this as literal drop-in code if operating within a strict Next.js App Router environment; adapt the states, hooks, and standard tags to the host framework's paradigms.import React, { useState, useEffect, useRef } from 'react';
import { 
  MessageSquare, Tv, BookOpen, Folder, Settings, Volume2, Database, Search, 
  Plus, Send, Play, Pause, Users, X, Cpu, TrendingUp, HardDrive, Book, FileText, 
  Sun, Moon, Maximize2, Terminal, ShieldAlert, Info, ChevronRight, Activity, 
  FolderPlus, ArrowUp, Sliders, Sparkles, Hash, Film, Clapperboard, Radio, Music, Bot, Loader2
} from 'lucide-react';

export default function App() {
  // Theme and Main Layout States
  const [isDarkMode, setIsDarkMode] = useState(true);
  const [activeModule, setActiveModule] = useState('chat');
  const [inVoiceChannel, setInVoiceChannel] = useState(false);
  const [activeVoiceRoom, setActiveVoiceRoom] = useState(null);
  
  // Example State for Chat
  const [activeChannel, setActiveChannel] = useState('general');
  const [chatMessages, setChatMessages] = useState([
    { id: 1, user: 'solon', role: 'founder', content: 'Welcome to Telos.', timestamp: '10:14 AM' }
  ]);
  const [newMessage, setNewMessage] = useState('');

  // The 4-Pane Global Layout
  return (
    <div className={`min-h-screen font-sans flex flex-col transition-colors duration-300 ${
      isDarkMode ? 'bg-[#0B0D13] text-[#F8FAFC]' : 'bg-[#F1F5F9] text-[#111827]'
    }`}>
      
      {/* 1. GLOBAL SYSTEM BAR (TOP) */}
      <header className={`h-14 border-b px-6 flex items-center justify-between z-25 transition-colors ${
        isDarkMode ? 'bg-[#121620] border-slate-800' : 'bg-[#FFFFFF] border-slate-200 shadow-sm'
      }`}>
        <div className="flex items-center">
          <div className="flex items-center space-x-2 w-48">
            {/* The Sovereign Ouroboros SVG implementation */}
            <div className="relative w-10 h-10 flex items-center justify-center">
              <svg viewBox="0 0 100 100" className={`w-10 h-10 ${isDarkMode ? 'text-[#F8FAFC]' : 'text-slate-900'}`}>
                <path d="M 33 14 A 40 40 0 1 1 12 40" fill="none" stroke="currentColor" strokeWidth="4.5" strokeLinecap="round" />
                <path d="M 34 14 C 26 10, 18 16, 21 24 C 24 29, 32 27, 36 22 C 39 18, 38 15, 34 14 Z" fill="currentColor" />
                <circle cx="26" cy="19" r="1.5" fill={isDarkMode ? '#0B0D13' : '#FFFFFF'} />
                <text x="52" y="57" textAnchor="middle" fontSize="24" fontFamily="sans-serif" letterSpacing="0.5" fill="currentColor">Telos</text>
              </svg>
            </div>
          </div>
        </div>

        {/* Central Search Bar */}
        <div className="hidden md:flex flex-1 max-w-xl mx-8">
          <div className="relative w-full group">
            <Search className="w-4 h-4 absolute left-4 top-2 text-slate-400 group-focus-within:text-cyan-500 transition-colors" />
            <input 
              type="text" 
              placeholder="Title, Author, Series, Genre, or Tags..."
              className={`w-full pl-11 pr-4 py-1.5 text-sm rounded border transition-all focus:outline-none ${
                isDarkMode 
                  ? 'bg-[#1A1E29] border-slate-700 text-[#F8FAFC] placeholder-slate-500 focus:border-cyan-500/50 focus:bg-[#1E2330]' 
                  : 'bg-slate-100 border-slate-300 text-[#111827] placeholder-slate-500 focus:border-cyan-400 focus:bg-white'
              }`}
            />
          </div>
        </div>
        
        {/* Quick Actions (Right) */}
        <div className="flex items-center space-x-3 w-48 justify-end">
           {/* Actions omitted for brevity in structural template */}
        </div>
      </header>

      {/* CORE WORKSPACE PANELS */}
      <div className="flex-1 flex overflow-hidden">
        
        {/* 2. GLOBAL NAV BAR (LEFT-MOST STRIP) */}
        <nav className={`w-[72px] flex flex-col items-center py-4 justify-between transition-colors flex-shrink-0 border-r ${
          isDarkMode ? 'bg-[#10131C] border-slate-800' : 'bg-[#F8FAFC] border-slate-200'
        }`}>
          {/* Module Switchers go here (Chat, Stream, Books, Files) */}
        </nav>

        {/* 3. CONTEXT SIDEBAR (ADAPTS TO ACTIVE MODULE) */}
        <aside className={`w-64 flex flex-col flex-shrink-0 border-r transition-colors ${
          isDarkMode ? 'bg-[#151924] border-slate-800' : 'bg-white border-slate-200'
        }`}>
          <div className="flex-1 overflow-y-auto py-3">
             {/* Sub-navigation logic goes here based on `activeModule` */}
          </div>
        </aside>

        {/* 4. THE CENTRAL ARENA */}
        <main className="flex-1 flex flex-col overflow-hidden">
           {/* Primary Module Components (Chat Stream, Media Player, PDF Reader) load here */}
           {activeModule === 'chat' && (
             <div className="flex-1 flex flex-col overflow-hidden">
                {/* Chat Implementation */}
             </div>
           )}
        </main>

        {/* 5. CONTEXTUAL RIGHT SIDEBAR (SLA / PEERS) */}
        <aside className={`w-64 hidden xl:flex flex-col flex-shrink-0 border-l transition-colors ${
          isDarkMode ? 'bg-[#121621] border-slate-800' : 'bg-white border-slate-200'
        }`}>
           {/* Server Specs, Slogans, and Online Peers render here */}
        </aside>

      </div>
    </div>
  );
}
