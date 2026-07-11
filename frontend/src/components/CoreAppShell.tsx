"use client";

import React, { useState, useEffect, useRef } from 'react';
import type Hls from 'hls.js';
import { ConnectionState } from 'livekit-client';
import {
  Activity, BookOpen, Folder, MessageSquare, Moon, Search, Settings,
  Sparkles, Sun, Tv, Waves, Hash, Volume2, Bot, Send, Trash2,
  Check, Mic, MicOff, PhoneOff, Globe, ChevronDown, Loader2,
  Download, FileText, Play, Pause
} from 'lucide-react';
import { useThemeStore, Theme } from '../stores/useThemeStore';
import { useVoiceSessionStore } from '../stores/useVoiceSessionStore';

// Placeholder identity until real authentication lands; overridable per deploy.
const CURRENT_USER = process.env.NEXT_PUBLIC_TELOS_USER ?? 'cleadmon';

const MODULES = [
  { id: 'chat', icon: MessageSquare, label: 'Chat' },
  { id: 'stream', icon: Tv, label: 'Stream' },
  { id: 'books', icon: BookOpen, label: 'Books' },
  { id: 'files', icon: Folder, label: 'Files' },
] as const;

type ModuleId = (typeof MODULES)[number]['id'];

const THEMES: Theme[] = ['light', 'dark', 'vaporwave'];
const THEME_ICONS = {
  light: Sun,
  dark: Moon,
  vaporwave: Waves,
};

interface Message {
  id: string;
  sender: string;
  avatar: string;
  role: 'Host' | 'Admin' | 'Member';
  content: string;
  timestamp: string;
}

interface MediaPlayableItem {
  id: string;
  title: string;
  duration: string;
  type: string;
}

export const CoreAppShell: React.FC = () => {
  const { theme, setTheme } = useThemeStore();
  const {
    connectionStatus,
    activeChannelId,
    isMuted,
    remoteParticipants,
    joinVoiceRoom,
    terminateVoiceSession,
    toggleMicrophone,
    audioInputs,
    selectedAudioInputId,
    updateAudioInputs,
    setAudioInput,
  } = useVoiceSessionStore();

  const [activeModule, setActiveModule] = useState<ModuleId>('chat');
  const [searchQuery, setSearchQuery] = useState('');
  const [manifestoOpen, setManifestoOpen] = useState(false);
  const [selectedChannel, setSelectedChannel] = useState('general');
  const [messages, setMessages] = useState<Message[]>([
    {
      id: '1',
      sender: 'cleadmon',
      avatar: 'CL',
      role: 'Host',
      content: 'welcome to telos! this is a self-hosted sovereign space.',
      timestamp: '10:42 am'
    },
    {
      id: '2',
      sender: 'oracle',
      avatar: 'AI',
      role: 'Admin',
      content: 'i can summarize chat histories or analyze chapters for you. click the spark buttons above.',
      timestamp: '10:43 am'
    }
  ]);
  const [inputText, setInputText] = useState('');
  const [successMsg, setSuccessMsg] = useState<string | null>(null);
  const [isSummarizing, setIsSummarizing] = useState(false);
  const [summaryText, setSummaryText] = useState<string | null>(null);

  // Voice settings & diagnostics state
  const [voiceSettingsOpen, setVoiceSettingsOpen] = useState(false);
  const [micTestActive, setMicTestActive] = useState(false);
  const [micLevel, setMicLevel] = useState(0);
  const micStreamRef = useRef<MediaStream | null>(null);
  const micAudioCtxRef = useRef<AudioContext | null>(null);
  const micAnimFrameRef = useRef<number | null>(null);
  const [diagResults, setDiagResults] = useState<{step: string; status: 'pending' | 'ok' | 'fail'; detail?: string}[]>([]);
  const [diagRunning, setDiagRunning] = useState(false);

  const searchInputRef = useRef<HTMLInputElement>(null);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const socketRef = useRef<WebSocket | null>(null);

  // Media Streaming State
  const [libraries, setLibraries] = useState<{ id: string; name: string; type: 'video' | 'audio' }[]>([]);
  const [selectedLibraryId, setSelectedLibraryId] = useState<string | null>(null);
  const [libraryItems, setLibraryItems] = useState<MediaPlayableItem[]>([]);
  const [playingItem, setPlayingItem] = useState<MediaPlayableItem | null>(null);

  const [isPlaying, setIsPlaying] = useState(false);
  const [currentTime, setCurrentTime] = useState(0);
  const [mediaDuration, setMediaDuration] = useState(0);

  const videoRef = useRef<HTMLVideoElement | HTMLAudioElement | null>(null);
  const hlsRef = useRef<Hls | null>(null);

  // Fetch media libraries
  useEffect(() => {
    if (activeModule !== 'stream') return;

    const fetchLibraries = async () => {
      try {
        const host = window.location.port === '3000' ? `${window.location.hostname}:8080` : window.location.host;
        const protocolHttp = window.location.protocol === 'https:' ? 'https:' : 'http:';
        const res = await fetch(`${protocolHttp}//${host}/api/v1/media`);
        if (res.ok) {
          const data = await res.json();
          setLibraries(data);
          if (data.length > 0 && !selectedLibraryId) {
            setSelectedLibraryId(data[0].id);
          }
        }
      } catch (err) {
        console.error('Failed to fetch media libraries:', err);
      }
    };
    fetchLibraries();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeModule]);

  // Fetch media items for selected library
  useEffect(() => {
    if (activeModule !== 'stream' || !selectedLibraryId) return;

    const fetchItems = async () => {
      try {
        const host = window.location.port === '3000' ? `${window.location.hostname}:8080` : window.location.host;
        const protocolHttp = window.location.protocol === 'https:' ? 'https:' : 'http:';
        const res = await fetch(`${protocolHttp}//${host}/api/v1/media/items?parentId=${selectedLibraryId}`);
        if (res.ok) {
          const data = await res.json();
          setLibraryItems(data);
        }
      } catch (err) {
        console.error('Failed to fetch library items:', err);
      }
    };
    fetchItems();
  }, [selectedLibraryId, activeModule]);

  // Initialize and attach HLS.js player
  useEffect(() => {
    if (activeModule !== 'stream' || !playingItem || !videoRef.current) return;

    const videoElement = videoRef.current;
    const host = window.location.port === '3000' ? `${window.location.hostname}:8080` : window.location.host;
    const protocolHttp = window.location.protocol === 'https:' ? 'https:' : 'http:';

    let streamUrl = "";
    if (playingItem.id.startsWith("http://") || playingItem.id.startsWith("https://")) {
      streamUrl = playingItem.id;
    } else if (playingItem.type === 'Audio' || playingItem.type === 'Audiobook') {
      streamUrl = `${protocolHttp}//${host}/api/v1/stream/audio/${playingItem.id}`;
    } else {
      streamUrl = `${protocolHttp}//${host}/api/v1/stream/video/${playingItem.id}`;
    }

    if (hlsRef.current) {
      hlsRef.current.destroy();
      hlsRef.current = null;
    }

    const isAudio = playingItem.type === 'Audio' || playingItem.type === 'Audiobook';
    const isNativeMp4 = playingItem.id === 'raising-helen' || playingItem.id === 'code-sovereignty' || streamUrl.toLowerCase().endsWith('.mp4');

    if (!isAudio) {
      if (isNativeMp4) {
        console.log("Playing native MP4 directly:", streamUrl);
        videoElement.src = streamUrl;
        videoElement.play().catch(e => console.log("Native playback failed:", e));
      } else {
        import('hls.js').then(({ default: Hls }) => {
          if (Hls.isSupported()) {
            const hls = new Hls();
            hlsRef.current = hls;
            hls.loadSource(streamUrl);
            hls.attachMedia(videoElement as HTMLVideoElement);
            hls.on(Hls.Events.MANIFEST_PARSED, () => {
              videoElement.play().catch(e => console.log("Play failed:", e));
            });
            hls.on(Hls.Events.ERROR, (event, data) => {
              if (data.fatal) {
                switch (data.type) {
                  case Hls.ErrorTypes.NETWORK_ERROR:
                    hls.startLoad();
                    break;
                  case Hls.ErrorTypes.MEDIA_ERROR:
                    hls.recoverMediaError();
                    break;
                  default:
                    hls.destroy();
                    hlsRef.current = null;
                    console.log("HLS playback failed, falling back to native video source:", streamUrl);
                    videoElement.src = streamUrl;
                    videoElement.play().catch(e => console.log("Native playback fallback failed:", e));
                    break;
                }
              }
            });
          } else if (videoElement.canPlayType('application/vnd.apple.mpegurl')) {
            videoElement.src = streamUrl;
            videoElement.addEventListener('loadedmetadata', () => {
              videoElement.play().catch(e => console.log("Play failed:", e));
            });
          } else {
            videoElement.src = streamUrl;
            videoElement.play().catch(e => console.log("Play failed:", e));
          }
        });
      }
    } else {
      videoElement.src = streamUrl;
      videoElement.play().catch(e => console.log("Play failed:", e));
    }

    return () => {
      if (hlsRef.current) {
        hlsRef.current.destroy();
        hlsRef.current = null;
      }
      videoElement.src = "";
    };
  }, [playingItem, activeModule]);

  const togglePlay = () => {
    if (!videoRef.current) return;
    if (isPlaying) {
      videoRef.current.pause();
    } else {
      videoRef.current.play().catch(e => console.log("Play failed:", e));
    }
  };

  const handleTimeUpdate = () => {
    if (videoRef.current) {
      setCurrentTime(videoRef.current.currentTime);
    }
  };

  const handleDurationChange = () => {
    if (videoRef.current) {
      setMediaDuration(videoRef.current.duration);
    }
  };

  const handleSeek = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (videoRef.current) {
      const seekTime = parseFloat(e.target.value);
      videoRef.current.currentTime = seekTime;
      setCurrentTime(seekTime);
    }
  };

  const formatTime = (timeInSeconds: number) => {
    if (isNaN(timeInSeconds)) return "00:00";
    const hrs = Math.floor(timeInSeconds / 3600);
    const mins = Math.floor((timeInSeconds % 3600) / 60);
    const secs = Math.floor(timeInSeconds % 60);

    const minsStr = mins < 10 ? `0${mins}` : `${mins}`;
    const secsStr = secs < 10 ? `0${secs}` : `${secs}`;

    if (hrs > 0) {
      return `${hrs}:${minsStr}:${secsStr}`;
    }
    return `${minsStr}:${secsStr}`;
  };

  // Success indicator timeout
  useEffect(() => {
    if (successMsg) {
      const timer = setTimeout(() => setSuccessMsg(null), 2000);
      return () => clearTimeout(timer);
    }
  }, [successMsg]);

  // Scroll to bottom of chat
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  // Reset chat history synchronously when the channel changes (state adjustment
  // during render instead of setState inside the effect below).
  const [renderedChannel, setRenderedChannel] = useState(selectedChannel);
  if (renderedChannel !== selectedChannel) {
    setRenderedChannel(selectedChannel);
    setMessages([]);
  }

  // WebSocket Live Connection
  useEffect(() => {
    const localWelcomeMessages: Record<string, Message[]> = {
      'general': [
        { id: 'welcome-gen', sender: 'telos-bot', avatar: 'TB', role: 'Admin', content: 'Welcome to #general! Feel free to converse here. Slogan: "your server, your community".', timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }).toLowerCase() }
      ],
      'dev-chat': [
        { id: 'welcome-dev', sender: 'oracle', avatar: 'AI', role: 'Host', content: 'Welcome to #dev-chat. Maintain containers boundaries and verify integrations.', timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }).toLowerCase() }
      ],
      'catalog-updates': [
        { id: 'welcome-cat', sender: 'telos-bot', avatar: 'TB', role: 'Admin', content: 'Welcome to #catalog-updates. Grimmory watchers and Jellyfin catalog routines logging here.', timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }).toLowerCase() }
      ]
    };

    let historyLoaded = false;
    const timer = setTimeout(() => {
      if (!historyLoaded) {
        setMessages((prev) => {
          if (prev.length > 0) {
            return prev;
          }
          return localWelcomeMessages[selectedChannel] || [];
        });
      }
    }, 500);

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const host = window.location.port === '3000' ? `${window.location.hostname}:8080` : window.location.host;
    const wsUrl = `${protocol}//${host}/api/v1/chat/ws?channel=${selectedChannel}&user=${CURRENT_USER}`;

    const socket = new WebSocket(wsUrl);
    socketRef.current = socket;

    socket.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        if (data.type === 'history') {
          historyLoaded = true;
          clearTimeout(timer);
          if (data.messages && data.messages.length > 0) {
            setMessages(data.messages);
          } else {
            setMessages(localWelcomeMessages[selectedChannel] || []);
          }
        } else if (data.type === 'message' && data.message) {
          setMessages((prev) => {
            if (prev.some((m) => m.id === data.message.id)) {
              return prev;
            }
            return [...prev, data.message];
          });
        }
      } catch (err) {
        console.error('Failed to parse WebSocket message:', err);
      }
    };

    return () => {
      clearTimeout(timer);
      socket.close();
    };
  }, [selectedChannel]);

  // Keyboard Navigation listener
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Ctrl/Cmd + K: Focus search
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        searchInputRef.current?.focus();
      }
      // Ctrl/Cmd + 1..4: Switch modules
      if ((e.ctrlKey || e.metaKey) && ['1', '2', '3', '4'].includes(e.key)) {
        e.preventDefault();
        const index = parseInt(e.key) - 1;
        if (index >= 0 && index < MODULES.length) {
          setActiveModule(MODULES[index].id);
        }
      }
      // Ctrl/Cmd + Shift + M: Toggle Mute
      if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === 'm') {
        e.preventDefault();
        if (connectionStatus === ConnectionState.Connected) {
          toggleMicrophone();
        }
      }
      // Esc: Close overlay
      if (e.key === 'Escape') {
        setManifestoOpen(false);
        setSummaryText(null);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [connectionStatus, toggleMicrophone]);

  const handleSendMessage = (e: React.FormEvent) => {
    e.preventDefault();
    if (!inputText.trim()) return;

    if (socketRef.current && socketRef.current.readyState === WebSocket.OPEN) {
      socketRef.current.send(JSON.stringify({ content: inputText.trim() }));
    } else {
      // Local fallback simulator if socket is not open
      const newMsg: Message = {
        id: Date.now().toString(),
        sender: CURRENT_USER,
        avatar: 'CL',
        role: 'Host',
        content: inputText.trim(),
        timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }).toLowerCase()
      };
      setMessages(prev => [...prev, newMsg]);

      setTimeout(() => {
        const botMsg: Message = {
          id: (Date.now() + 1).toString(),
          sender: 'telos-bot',
          avatar: 'TB',
          role: 'Admin',
          content: `echo: ${newMsg.content}`,
          timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }).toLowerCase()
        };
        setMessages(prev => [...prev, botMsg]);
      }, 1000);
    }
    setInputText('');
  };

  const triggerChatSummary = () => {
    setIsSummarizing(true);
    setTimeout(() => {
      setIsSummarizing(false);
      setSummaryText('AI Summary: Users discussed Digital Sovereignty, confirmed successful establishment of the Traefik configuration, local pgx/v5 Go API, and Next.js shell with zero flat-rule compliance errors.');
      setSuccessMsg('Summary Generated');
    }, 1500);
  };

  // Join voice channel action
  const handleVoiceJoinToggle = async (channelId: string) => {
    if (activeChannelId === channelId) {
      await terminateVoiceSession();
    } else {
      try {
        setSuccessMsg(`Connecting to ${channelId}...`);

        const host = window.location.port === '3000' ? `${window.location.hostname}:8080` : window.location.host;
        const protocolHttp = window.location.protocol === 'https:' ? 'https:' : 'http:';

        // Fetch LiveKit join token from backend
        const tokenRes = await fetch(`${protocolHttp}//${host}/api/v1/voice/token?room=${channelId}&user=${CURRENT_USER}`);
        if (!tokenRes.ok) {
          throw new Error(`Failed to fetch voice token: ${tokenRes.statusText}`);
        }
        const data = await tokenRes.json();
        const token = data.token;

        // Build gateway URL targeting the Go backend's /livekit reverse proxy
        const protocolWs = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const gatewayUrl = `${protocolWs}//${host}/livekit`;

        await joinVoiceRoom(gatewayUrl, token, channelId);
        setSuccessMsg(`Joined ${channelId}`);
      } catch (err) {
        console.error('Failed to join voice room:', err);
        setSuccessMsg('Voice Connection Failed');
      }
    }
  };

  // ---- Microphone test helpers ----
  const startMicTest = async () => {
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      micStreamRef.current = stream;
      const audioCtx = new AudioContext();
      micAudioCtxRef.current = audioCtx;
      const source = audioCtx.createMediaStreamSource(stream);
      const analyser = audioCtx.createAnalyser();
      analyser.fftSize = 256;
      source.connect(analyser);
      const dataArray = new Uint8Array(analyser.frequencyBinCount);

      const tick = () => {
        analyser.getByteFrequencyData(dataArray);
        let sum = 0;
        for (let i = 0; i < dataArray.length; i++) sum += dataArray[i];
        const avg = sum / dataArray.length;
        setMicLevel(Math.min(avg / 128, 1)); // normalize 0-1
        micAnimFrameRef.current = requestAnimationFrame(tick);
      };
      tick();
      setMicTestActive(true);
    } catch (err) {
      console.error('Microphone test failed:', err);
      setSuccessMsg('Mic access denied');
    }
  };

  const stopMicTest = () => {
    if (micAnimFrameRef.current) {
      cancelAnimationFrame(micAnimFrameRef.current);
      micAnimFrameRef.current = null;
    }
    if (micStreamRef.current) {
      micStreamRef.current.getTracks().forEach((t) => t.stop());
      micStreamRef.current = null;
    }
    if (micAudioCtxRef.current) {
      micAudioCtxRef.current.close();
      micAudioCtxRef.current = null;
    }
    setMicLevel(0);
    setMicTestActive(false);
  };

  // ---- Connection diagnostics ----
  const runConnectionDiag = async () => {
    setDiagRunning(true);
    const host = window.location.port === '3000' ? `${window.location.hostname}:8080` : window.location.host;
    const protocolHttp = window.location.protocol === 'https:' ? 'https:' : 'http:';
    const steps: {step: string; status: 'pending' | 'ok' | 'fail'; detail?: string}[] = [
      { step: 'Backend Health', status: 'pending' },
      { step: 'Voice Token', status: 'pending' },
      { step: 'LiveKit Gateway', status: 'pending' },
    ];
    setDiagResults([...steps]);

    // Step 1: Health
    try {
      const res = await fetch(`${protocolHttp}//${host}/api/v1/health`);
      steps[0] = { step: 'Backend Health', status: res.ok ? 'ok' : 'fail', detail: `HTTP ${res.status}` };
    } catch (err) {
      steps[0] = { step: 'Backend Health', status: 'fail', detail: String(err) };
    }
    setDiagResults([...steps]);

    // Step 2: Token
    let token = '';
    try {
      const res = await fetch(`${protocolHttp}//${host}/api/v1/voice/token?room=diag-test&user=${CURRENT_USER}`);
      if (res.ok) {
        const data = await res.json();
        token = data.token;
        steps[1] = { step: 'Voice Token', status: 'ok', detail: 'Token received' };
      } else {
        steps[1] = { step: 'Voice Token', status: 'fail', detail: `HTTP ${res.status}` };
      }
    } catch (err) {
      steps[1] = { step: 'Voice Token', status: 'fail', detail: String(err) };
    }
    setDiagResults([...steps]);

    // Step 3: LiveKit signaling
    if (token) {
      try {
        const protocolWs = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocolWs}//${host}/livekit/rtc?access_token=${token}`;
        const ws = new WebSocket(wsUrl);
        const result = await new Promise<'ok' | 'fail'>((resolve) => {
          const timeout = setTimeout(() => { ws.close(); resolve('fail'); }, 5000);
          ws.onopen = () => { clearTimeout(timeout); ws.close(); resolve('ok'); };
          ws.onerror = () => { clearTimeout(timeout); resolve('fail'); };
        });
        steps[2] = { step: 'LiveKit Gateway', status: result, detail: result === 'ok' ? 'WebSocket opened' : 'Connection refused' };
      } catch (err) {
        steps[2] = { step: 'LiveKit Gateway', status: 'fail', detail: String(err) };
      }
    } else {
      steps[2] = { step: 'LiveKit Gateway', status: 'fail', detail: 'Skipped (no token)' };
    }
    setDiagResults([...steps]);
    setDiagRunning(false);
  };

  // Clean up mic test resources when settings panel closes
  useEffect(() => {
    if (voiceSettingsOpen) return;
    // Release mic resources without calling setState synchronously in the effect
    if (micAnimFrameRef.current) {
      cancelAnimationFrame(micAnimFrameRef.current);
      micAnimFrameRef.current = null;
    }
    if (micStreamRef.current) {
      micStreamRef.current.getTracks().forEach((t) => t.stop());
      micStreamRef.current = null;
    }
    if (micAudioCtxRef.current) {
      micAudioCtxRef.current.close();
      micAudioCtxRef.current = null;
    }
    return () => {
      // State reset deferred to cleanup to avoid synchronous setState in effect
      setMicLevel(0);
      setMicTestActive(false);
    };
  }, [voiceSettingsOpen]);

  // Refresh audio inputs when the settings panel opens
  useEffect(() => {
    if (voiceSettingsOpen) {
      updateAudioInputs();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [voiceSettingsOpen]);

  const nextTheme = THEMES[(THEMES.indexOf(theme) + 1) % THEMES.length];
  const NextThemeIcon = THEME_ICONS[nextTheme];

  return (
    <div className="flex h-screen w-screen flex-col overflow-hidden bg-app-bg text-text-primary antialiased selection:bg-accent/30">
      {/* 1. Global Header */}
      <header className="z-20 flex h-14 items-center justify-between border-b border-border-color bg-panel-bg px-6 transition-colors duration-150">
        <div className="flex w-48 items-center gap-2">
          {/* Logo Composition Rule */}
          <svg viewBox="0 0 100 100" className={`h-10 w-10 text-accent ${theme === 'vaporwave' ? 'vaporwave-glow' : ''}`} role="img" aria-label="Telos ouroboros mark">
            <path d="M 33 14 A 40 40 0 1 1 12 40" fill="none" stroke="currentColor" strokeWidth="4.5" strokeLinecap="round" />
            <path d="M 34 14 C 26 10, 18 16, 21 24 C 24 29, 32 27, 36 22 C 39 18, 38 15, 34 14 Z" fill="currentColor" />
            <circle cx="26" cy="19" r="1.5" className="fill-app-bg" />
          </svg>
          <span className="text-lg font-semibold tracking-wide">Telos</span>
        </div>

        {/* Global Search */}
        <div className="mx-8 hidden max-w-xl flex-1 md:flex">
          <div className="group relative w-full">
            <Search className="absolute left-4 top-2.5 h-4 w-4 text-text-secondary transition-colors group-focus-within:text-accent" />
            <input
              ref={searchInputRef}
              type="text"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder="Title, Author, Series, Genre, or Tags..."
              className="w-full rounded-lg border border-border-color bg-input-bg py-1.5 pl-11 pr-4 text-sm text-text-primary placeholder-text-secondary transition-all outline-none focus:border-accent/50 focus-visible:ring-2 focus-visible:ring-focus-ring focus-visible:ring-offset-2 focus-visible:ring-offset-app-bg"
            />
          </div>
        </div>

        {/* Actions & Controls */}
        <div className="flex items-center justify-end gap-2 md:w-64">
          <button
            onClick={() => setManifestoOpen(true)}
            className="flex items-center gap-1.5 rounded-lg border border-border-color bg-app-bg px-2.5 py-1 text-xs font-semibold text-text-secondary hover:text-text-primary hover:bg-accent-hover transition-colors"
          >
            <FileText className="h-3.5 w-3.5" />
            <span className="hidden sm:inline">creed</span>
          </button>

          <button aria-label="Activity" className="rounded-lg p-2 text-text-secondary hover:text-text-primary hover:bg-accent-hover transition-colors">
            <Activity className="h-4 w-4" />
          </button>
          <button aria-label="AI Oracle" className="rounded-lg p-2 text-text-secondary hover:text-text-primary hover:bg-accent-hover transition-colors">
            <Sparkles className="h-4 w-4" />
          </button>
          <button aria-label="Settings" className="rounded-lg p-2 text-text-secondary hover:text-text-primary hover:bg-accent-hover transition-colors">
            <Settings className="h-4 w-4" />
          </button>

          {/* Theme Selector */}
          <button
            aria-label={`Switch theme (next: ${nextTheme})`}
            onClick={() => setTheme(nextTheme)}
            className="rounded-lg p-2 text-text-secondary hover:text-text-primary hover:bg-accent-hover transition-colors"
          >
            <NextThemeIcon className="h-4 w-4" />
          </button>

          {/* Success toast indicator */}
          {successMsg && (
            <div className="flex items-center gap-1 text-xs text-accent-text ml-2">
              <Check className="h-3.5 w-3.5" />
              <span>{successMsg}</span>
            </div>
          )}
        </div>
      </header>

      {/* Main Content Area */}
      <div className="flex flex-1 overflow-hidden">
        {/* 2. Global Nav Strip */}
        <nav className="flex w-[72px] flex-shrink-0 flex-col items-center justify-between border-r border-border-color bg-panel-nav py-4 transition-colors duration-150">
          <div className="flex flex-col gap-2">
            {MODULES.map(({ id, icon: Icon, label }) => (
              <button
                key={id}
                aria-label={label}
                onClick={() => {
                  setActiveModule(id);
                  setSummaryText(null);
                }}
                className={`rounded-xl p-3 transition-colors ${
                  activeModule === id
                    ? 'bg-accent-wash text-accent-text'
                    : 'text-text-secondary hover:bg-accent-hover hover:text-text-primary'
                }`}
              >
                <Icon className="h-5 w-5" />
              </button>
            ))}
          </div>
          <button
            aria-label="Profile: AD"
            title="Local peer node details: connected to telos.local"
            className="flex h-10 w-10 items-center justify-center rounded-full bg-avatar-bg text-avatar-text text-xs font-semibold hover:border hover:border-accent transition-all"
          >
            AD
          </button>
        </nav>

        {/* 3. Contextual Sidebar */}
        <aside className="hidden w-64 flex-shrink-0 flex-col border-r border-border-color bg-panel-sidebar transition-colors duration-150 lg:flex">
          <div className="flex-1 overflow-y-auto p-4">
            {activeModule === 'chat' && (
              <div className="flex flex-col gap-6">
                <div>
                  <h3 className="text-[10px] font-bold uppercase tracking-wider text-text-secondary mb-2">Text Channels</h3>
                  <div className="flex flex-col gap-1">
                    {['general', 'dev-chat', 'catalog-updates'].map((ch) => (
                      <button
                        key={ch}
                        onClick={() => setSelectedChannel(ch)}
                        className={`flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-sm transition-colors ${
                          selectedChannel === ch
                            ? 'bg-accent-hover text-text-primary font-medium'
                            : 'text-text-secondary hover:bg-accent-hover/40 hover:text-text-primary'
                        }`}
                      >
                        <Hash className="h-4 w-4" />
                        <span>{ch}</span>
                      </button>
                    ))}
                  </div>
                </div>

                <div>
                  <h3 className="text-[10px] font-bold uppercase tracking-wider text-text-secondary mb-2">Voice Channels</h3>
                  <div className="flex flex-col gap-1">
                    {['voice-lounge', 'gaming-lounge'].map((vc) => {
                      const isThisActive = activeChannelId === vc;
                      const isConnecting = isThisActive && connectionStatus === ConnectionState.Connecting;
                      const isConnected = isThisActive && connectionStatus === ConnectionState.Connected;
                      return (
                        <div key={vc} className="flex flex-col">
                          <button
                            onClick={() => handleVoiceJoinToggle(vc)}
                            className={`flex items-center justify-between rounded-lg px-2.5 py-1.5 text-sm transition-colors ${
                              isThisActive
                                ? 'bg-accent-wash text-accent-text font-medium'
                                : 'text-text-secondary hover:bg-accent-hover/40 hover:text-text-primary'
                            }`}
                          >
                            <span className="flex items-center gap-2">
                              <Volume2 className="h-4 w-4" />
                              <span>{vc}</span>
                            </span>
                            <span className="flex items-center gap-1 text-[10px] uppercase font-bold tracking-wider">
                              {isConnecting && <Loader2 className="h-3 w-3 animate-spin" />}
                              {isThisActive ? 'Leave' : 'Join'}
                            </span>
                          </button>
                          {isThisActive && (
                            <div className="pl-8 pt-1 pb-2 flex flex-col gap-1">
                              {/* Local user */}
                              <div className="flex items-center gap-1.5 text-xs text-text-secondary">
                                {isConnecting ? (
                                  <Loader2 className="h-3 w-3 animate-spin text-text-secondary" />
                                ) : (
                                  <span className="h-1.5 w-1.5 rounded-full bg-accent animate-pulse" />
                                )}
                                <span>{CURRENT_USER} (you)</span>
                                {isConnected && isMuted && <MicOff className="h-3 w-3 text-text-tertiary" />}
                              </div>
                              {/* Remote participants */}
                              {remoteParticipants.map((p) => (
                                <div key={p.sid} className="flex items-center gap-1.5 text-xs text-text-secondary">
                                  <span className="h-1.5 w-1.5 rounded-full bg-accent" />
                                  <span>{p.identity || p.sid}</span>
                                </div>
                              ))}
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                </div>
              </div>
            )}

            {activeModule === 'stream' && (
              <div className="flex flex-col gap-4">
                <h3 className="text-[10px] font-bold uppercase tracking-wider text-text-secondary">Media Library</h3>
                <div className="flex flex-col gap-1 text-sm text-text-secondary">
                  {libraries.map((lib) => (
                    <button
                      key={lib.id}
                      onClick={() => setSelectedLibraryId(lib.id)}
                      className={`text-left rounded-lg p-2 hover:bg-accent-hover hover:text-text-primary transition-colors cursor-pointer w-full ${
                        selectedLibraryId === lib.id ? 'bg-accent-hover text-text-primary font-semibold' : ''
                      }`}
                    >
                      {lib.name}
                    </button>
                  ))}
                </div>
                <div className="mt-4 border-t border-border-color pt-4">
                  <h4 className="text-xs font-semibold mb-2">Ingest Stream</h4>
                  <input
                    type="text"
                    placeholder="https://..."
                    className="w-full rounded border border-border-color bg-input-bg p-1.5 text-xs outline-none focus:border-accent"
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' && e.currentTarget.value.trim() !== '') {
                        const urlStr = e.currentTarget.value.trim();
                        setPlayingItem({
                          id: urlStr,
                          title: urlStr.substring(urlStr.lastIndexOf('/') + 1) || "Live Feed",
                          duration: "Live",
                          type: "Video"
                        });
                        e.currentTarget.value = "";
                        setSuccessMsg("Ingested live feed");
                      }
                    }}
                  />
                </div>
              </div>
            )}

            {activeModule === 'books' && (
              <div className="flex flex-col gap-4">
                <h3 className="text-[10px] font-bold uppercase tracking-wider text-text-secondary">Grimmory</h3>
                <div className="flex flex-col gap-1 text-sm text-text-secondary">
                  <div className="rounded-lg p-2 hover:bg-accent-hover hover:text-text-primary cursor-pointer font-semibold text-text-primary">Dashboard</div>
                  <div className="rounded-lg p-2 hover:bg-accent-hover hover:text-text-primary cursor-pointer">All Books</div>
                  <div className="rounded-lg p-2 hover:bg-accent-hover hover:text-text-primary cursor-pointer">Authors</div>
                </div>
                <div className="mt-4 border-t border-border-color pt-4">
                  <h4 className="text-[10px] font-bold uppercase tracking-wider text-text-secondary mb-2">Shelves</h4>
                  <div className="flex items-center justify-between text-xs text-text-secondary p-1">
                    <span>read-stack</span>
                    <span className="rounded bg-accent-hover px-1.5 py-0.5 text-[10px]">12</span>
                  </div>
                </div>
              </div>
            )}

            {activeModule === 'files' && (
              <div className="flex flex-col gap-4">
                <h3 className="text-[10px] font-bold uppercase tracking-wider text-text-secondary">Storage Volumes</h3>
                <div className="flex flex-col gap-3 font-mono text-xs text-text-secondary">
                  <div>
                    <div className="flex justify-between mb-1">
                      <span>/mnt/storage/shared</span>
                      <span>42%</span>
                    </div>
                    <div className="h-1.5 w-full bg-accent-hover rounded-full overflow-hidden">
                      <div className="h-full bg-accent" style={{ width: '42%' }}></div>
                    </div>
                  </div>
                  <div>
                    <div className="flex justify-between mb-1">
                      <span>/mnt/storage/books</span>
                      <span>68%</span>
                    </div>
                    <div className="h-1.5 w-full bg-accent-hover rounded-full overflow-hidden">
                      <div className="h-full bg-accent" style={{ width: '68%' }}></div>
                    </div>
                  </div>
                </div>
              </div>
            )}
          </div>

          {/* Contextual Sidebar Footer (shell-owned) */}
          <div className="border-t border-border-color p-4 bg-panel-nav text-[10px] font-mono text-text-secondary flex flex-col gap-1">
            <div className="flex items-center gap-1.5">
              <Globe className="h-3 w-3 text-accent" />
              <span>tunnel: encrypted</span>
            </div>
            <div>node: telos-node-1</div>
          </div>
        </aside>

        {/* 4. Central Arena */}
        <main className="relative flex min-w-0 flex-1 flex-col bg-app-bg transition-colors duration-150">
          {activeModule === 'chat' && (
            <div className="flex flex-1 flex-col overflow-hidden">
              {/* Central Arena Header */}
              <div className="flex h-14 items-center justify-between border-b border-border-color bg-panel-bg px-6">
                <div className="flex items-center gap-2">
                  <Hash className="h-5 w-5 text-text-secondary" />
                  <span className="font-semibold">{selectedChannel}</span>
                </div>
                <button
                  onClick={triggerChatSummary}
                  disabled={isSummarizing}
                  className="flex items-center gap-1.5 rounded-lg bg-accent text-white px-3 py-1.5 text-xs font-semibold transition-colors hover:bg-accent/80 disabled:opacity-50"
                >
                  <Bot className="h-4 w-4" />
                  {isSummarizing ? 'Summarizing...' : 'Summarize Missed'}
                </button>
              </div>

              {/* Chat Brand Banner */}
              <div className="border-b border-border-color bg-accent-wash px-6 py-2 text-center text-xs font-mono text-accent-text tracking-wide lowercase">
                be on the net, but not of the net
              </div>

              {/* Chat History */}
              <div className="flex-1 overflow-y-auto p-6 flex flex-col gap-4">
                {messages.map((msg) => (
                  <div key={msg.id} className="flex gap-3 text-sm items-start">
                    <div className="flex h-9 w-9 items-center justify-center rounded-full bg-accent-hover text-accent font-semibold text-xs shrink-0">
                      {msg.avatar}
                    </div>
                    <div className="flex flex-col">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-text-primary">{msg.sender}</span>
                        {msg.role !== 'Member' && (
                          <span className="rounded bg-accent-wash px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider text-accent-text">
                            {msg.role}
                          </span>
                        )}
                        <span className="text-[10px] text-text-tertiary font-mono">{msg.timestamp}</span>
                      </div>
                      <p className="mt-1 text-text-secondary break-all">{msg.content}</p>
                    </div>
                  </div>
                ))}
                <div ref={messagesEndRef} />
              </div>

              {/* Chat Input Container */}
              <form onSubmit={handleSendMessage} className="border-t border-border-color bg-panel-bg p-4 flex gap-2">
                <input
                  type="text"
                  value={inputText}
                  onChange={(e) => setInputText(e.target.value)}
                  placeholder={`Message #${selectedChannel}...`}
                  className="flex-1 rounded-lg border border-border-color bg-input-bg px-4 py-2 text-sm text-text-primary placeholder-text-secondary outline-none focus:border-accent"
                />
                <button
                  type="submit"
                  aria-label="Send message"
                  className="rounded-lg bg-accent text-white p-2 hover:bg-accent/80 transition-colors"
                >
                  <Send className="h-4 w-4" />
                </button>
              </form>
            </div>
          )}

          {activeModule === 'stream' && (
            <div className="flex flex-1 flex-col p-8 overflow-y-auto">
              <div className="w-full max-w-4xl mx-auto flex flex-col gap-6">
                
                {/* Media Player */}
                <div className="border border-border-color bg-panel-bg rounded-xl overflow-hidden p-6 flex flex-col gap-4">
                  <div className="flex justify-between items-center">
                    <h2 className="text-base font-bold tracking-wide">
                      {playingItem ? `Streaming: ${playingItem.title}` : "Media Stream Player"}
                    </h2>
                    {playingItem && (
                      <span className="rounded bg-accent-wash px-2 py-0.5 text-[10px] font-mono font-bold uppercase tracking-wider text-accent-text">
                        {playingItem.type}
                      </span>
                    )}
                  </div>

                  <div className="relative aspect-video bg-black rounded-lg overflow-hidden flex items-center justify-center border border-border-color">
                    {playingItem ? (
                      playingItem.type === 'Audio' || playingItem.type === 'Audiobook' ? (
                        <div className="flex flex-col items-center justify-center gap-4 text-center">
                          <Volume2 className="h-16 w-16 text-accent animate-pulse" />
                          <audio
                            ref={videoRef as React.RefObject<HTMLAudioElement>}
                            className="hidden"
                            onTimeUpdate={handleTimeUpdate}
                            onDurationChange={handleDurationChange}
                            onPlay={() => setIsPlaying(true)}
                            onPause={() => setIsPlaying(false)}
                          />
                          <span className="text-sm font-semibold">{playingItem.title}</span>
                        </div>
                      ) : (
                        <video
                          ref={videoRef as React.RefObject<HTMLVideoElement>}
                          className="w-full h-full object-contain"
                          onTimeUpdate={handleTimeUpdate}
                          onDurationChange={handleDurationChange}
                          onPlay={() => setIsPlaying(true)}
                          onPause={() => setIsPlaying(false)}
                          playsInline
                        />
                      )
                    ) : (
                      <div className="text-text-secondary font-mono text-xs flex flex-col items-center gap-2">
                        <Tv className="h-12 w-12 text-text-tertiary" />
                        <span>Select a media item below to start playing</span>
                      </div>
                    )}
                  </div>

                  {playingItem && (
                    <div className="flex flex-col gap-3">
                      {/* Controls Bar */}
                      <div className="flex items-center gap-4">
                        <button
                          onClick={togglePlay}
                          aria-label={isPlaying ? "Pause" : "Play"}
                          className="rounded-lg bg-accent text-white p-2.5 hover:bg-accent/80 transition-colors cursor-pointer"
                        >
                          {isPlaying ? <Pause className="h-4 w-4" /> : <Play className="h-4 w-4" />}
                        </button>

                        {/* Scrub Timeline */}
                        <div className="flex-1 flex items-center gap-3">
                          <span className="text-[10px] font-mono text-text-secondary">
                            {formatTime(currentTime)}
                          </span>
                          <input
                            type="range"
                            min="0"
                            max={mediaDuration || 0}
                            value={currentTime}
                            onChange={handleSeek}
                            className="flex-1 h-1.5 bg-accent-hover rounded-lg appearance-none cursor-pointer accent-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus-ring"
                          />
                          <span className="text-[10px] font-mono text-text-secondary">
                            {formatTime(mediaDuration)}
                          </span>
                        </div>
                      </div>

                      <div className="flex justify-between items-center text-[10px] font-mono text-text-secondary border-t border-border-color pt-2">
                        <span>status: {isPlaying ? "playing" : "paused"}</span>
                        <span>bitrate: 4200 kbps (1080p)</span>
                      </div>
                    </div>
                  )}
                </div>

                {/* Media Catalog Items Grid */}
                <div>
                  <h3 className="text-xs uppercase font-bold tracking-wider text-text-secondary mb-3 border-b-2 border-accent w-fit pb-1">
                    Library Contents
                  </h3>
                  {libraryItems.length > 0 ? (
                    <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-4">
                      {libraryItems.map((item) => {
                        const isThisPlaying = playingItem?.id === item.id;
                        return (
                          <div
                            key={item.id}
                            onClick={() => setPlayingItem(item)}
                            className={`group relative flex flex-col border rounded-xl overflow-hidden cursor-pointer transition-all hover:border-accent/40 bg-panel-bg ${
                              isThisPlaying ? 'border-accent' : 'border-border-color'
                            }`}
                          >
                            <div className="h-28 bg-input-bg flex items-center justify-center text-text-secondary relative">
                              {item.type === 'Audio' || item.type === 'Audiobook' ? (
                                <Volume2 className="h-8 w-8 text-text-tertiary group-hover:text-accent transition-colors" />
                              ) : (
                                <Tv className="h-8 w-8 text-text-tertiary group-hover:text-accent transition-colors" />
                              )}
                              
                              {/* Play overlay on hover */}
                              <div className="absolute inset-0 bg-black/40 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity">
                                <div className="rounded-full bg-accent p-2 text-white">
                                  <Play className="h-4 w-4 fill-current" />
                                </div>
                              </div>

                              {/* Sovereign Blue progress bar flush to the bottom edge of the thumbnail */}
                              <div className="absolute bottom-0 left-0 h-1 bg-accent" style={{ width: isThisPlaying ? `${(currentTime / (mediaDuration || 1)) * 100}%` : '35%' }} />
                            </div>
                            <div className="p-3 flex flex-col gap-0.5">
                              <span className="font-semibold text-xs text-text-primary truncate" title={item.title}>
                                {item.title}
                              </span>
                              <span className="text-[10px] text-text-secondary font-mono">
                                {item.duration}
                              </span>
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  ) : (
                    <div className="text-center py-8 text-xs text-text-secondary border border-dashed border-border-color rounded-xl">
                      No items found in this library view
                    </div>
                  )}
                </div>

              </div>
            </div>
          )}

          {activeModule === 'books' && (
            <div className="flex flex-1 flex-col p-8 gap-8 overflow-y-auto">
              <div>
                <h2 className="text-xs uppercase font-bold tracking-wider text-text-secondary mb-3 border-b-2 border-accent w-fit pb-1">Continue Reading</h2>
                <div className="grid grid-cols-2 sm:grid-cols-3 gap-4">
                  <div className="border border-border-color bg-panel-bg rounded-xl p-4 flex flex-col gap-2 hover:border-accent/40 cursor-pointer">
                    <span className="rounded bg-accent-wash text-accent-text text-[9px] font-bold uppercase tracking-wider w-fit px-1.5 py-0.5">EPUB</span>
                    <span className="font-semibold text-sm">The Design Creed</span>
                    <span className="text-xs text-text-secondary">Telos Core</span>
                  </div>
                  <div className="border border-border-color bg-panel-bg rounded-xl p-4 flex flex-col gap-2 hover:border-accent/40 cursor-pointer">
                    <span className="rounded bg-accent-wash text-accent-text text-[9px] font-bold uppercase tracking-wider w-fit px-1.5 py-0.5">PDF</span>
                    <span className="font-semibold text-sm">Out of the Enclosure</span>
                    <span className="text-xs text-text-secondary">Sovereign Citizen</span>
                  </div>
                </div>
              </div>
            </div>
          )}

          {activeModule === 'files' && (
            <div className="flex flex-1 flex-col p-8 gap-4 overflow-y-auto">
              <div className="flex justify-between items-center">
                <div className="text-xs font-mono text-text-secondary">/data/shared/books</div>
                <div className="flex gap-2">
                  <button className="rounded-lg bg-accent text-white px-3 py-1.5 text-xs font-semibold hover:bg-accent/80 transition-colors">
                    Upload
                  </button>
                  <button className="rounded-lg border border-border-color bg-panel-bg text-text-primary px-3 py-1.5 text-xs font-semibold hover:bg-accent-hover transition-colors">
                    New Dir
                  </button>
                </div>
              </div>

              <div className="border border-border-color rounded-xl overflow-hidden bg-panel-bg">
                <table className="w-full text-left text-xs border-collapse">
                  <thead>
                    <tr className="border-b border-border-color text-text-secondary font-mono bg-panel-nav">
                      <th className="p-3">Name</th>
                      <th className="p-3">Size</th>
                      <th className="p-3">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr className="border-b border-border-color hover:bg-accent-hover/30">
                      <td className="p-3 font-semibold">creed.txt</td>
                      <td className="p-3 font-mono">1.2 KB</td>
                      <td className="p-3 flex gap-2">
                        <button aria-label="Download" className="text-accent hover:underline"><Download className="h-4 w-4" /></button>
                        <button aria-label="Delete" className="text-text-primary hover:underline"><Trash2 className="h-4 w-4" /></button>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {/* Chat Summary Modal Overlay */}
          {summaryText && (
            <div className="absolute inset-0 z-30 bg-app-bg/85 flex items-center justify-center p-6 transition-opacity duration-300">
              <div className="w-full max-w-md border border-border-color bg-panel-bg rounded-xl p-6 flex flex-col gap-4">
                <div className="flex justify-between items-center">
                  <span className="font-semibold flex items-center gap-1.5 text-accent">
                    <Bot className="h-4 w-4" />
                    AI Summary
                  </span>
                  <button onClick={() => setSummaryText(null)} className="text-xs text-text-secondary hover:text-text-primary">Close [Esc]</button>
                </div>
                <p className="text-sm text-text-secondary font-mono leading-relaxed">{summaryText}</p>
              </div>
            </div>
          )}
        </main>

        {/* 5. Server SLA Sidebar */}
        <aside className="hidden w-64 flex-shrink-0 flex-col border-l border-border-color bg-panel-sidebar transition-colors duration-150 xl:flex">
          <div className="flex-1 overflow-y-auto p-4 flex flex-col gap-6">
            <div>
              <h3 className="text-xs font-bold border-b border-border-color pb-1 mb-2">Sovereign Node SLA</h3>
              <div className="flex flex-col gap-1.5 text-xs font-mono text-text-secondary">
                <div className="flex justify-between">
                  <span>Host OS:</span>
                  <span>linux/amd64</span>
                </div>
                <div className="flex justify-between">
                  <span>DB Engine:</span>
                  <span>PostgreSQL 16</span>
                </div>
              </div>
            </div>

            <div className="flex flex-col gap-2">
              {/* Slogans verbatim and lowercase */}
              <div className="border border-border-color bg-panel-bg p-3 rounded-lg text-center text-xs font-mono tracking-wide text-text-secondary lowercase">
                your server, your community
              </div>
              <div className="border border-border-color bg-panel-bg p-3 rounded-lg text-center text-xs font-mono tracking-wide text-text-secondary lowercase">
                be on the net, but not of the net
              </div>
            </div>

            <div>
              <h4 className="text-[10px] font-bold uppercase tracking-wider text-text-secondary mb-2">Connected Peers</h4>
              <div className="flex flex-col gap-1 text-xs text-text-secondary">
                <div className="flex items-center gap-1.5">
                  <div className="h-1.5 w-1.5 rounded-full bg-accent" />
                  <span>peer-1.telos.local</span>
                </div>
                <div className="flex items-center gap-1.5">
                  <div className="h-1.5 w-1.5 rounded-full bg-accent" />
                  <span>peer-2.telos.local</span>
                </div>
              </div>
            </div>
          </div>
        </aside>
      </div>

      {/* Floating call bar (Voice status bar) at shell level — visible during Connecting AND Connected */}
      {connectionStatus !== ConnectionState.Disconnected && activeChannelId && (
        <div className={`absolute bottom-6 right-6 z-50 flex flex-col gap-2 rounded-xl border border-accent/20 bg-panel-bg p-4 ${theme === 'vaporwave' ? 'vaporwave-glow' : ''}`}>
          <div className="flex items-center gap-4">
            <div className="flex flex-col gap-0.5">
              {connectionStatus === ConnectionState.Connecting ? (
                <span className="flex items-center gap-1.5 text-[10px] font-bold uppercase tracking-wider text-text-secondary">
                  <Loader2 className="h-3 w-3 animate-spin" />
                  Connecting…
                </span>
              ) : (
                <span className="flex items-center gap-1.5 text-[10px] font-bold uppercase tracking-wider text-accent-text">
                  <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-accent motion-reduce:animate-none" />
                  Voice Active
                </span>
              )}
              <span className="font-mono text-xs text-text-secondary">{activeChannelId}</span>
            </div>
            <div className="flex items-center gap-2">
              {connectionStatus === ConnectionState.Connected && (
                <button
                  onClick={toggleMicrophone}
                  aria-label={isMuted ? 'Unmute microphone' : 'Mute microphone'}
                  className={`rounded-lg p-2 transition-colors ${
                    isMuted
                      ? 'bg-text-primary text-app-bg'
                      : 'bg-accent-hover text-text-primary hover:bg-accent-hover/70'
                  }`}
                >
                  {isMuted ? <MicOff className="h-4 w-4" /> : <Mic className="h-4 w-4" />}
                </button>
              )}
              <button
                onClick={() => setVoiceSettingsOpen((v) => !v)}
                aria-label="Voice settings"
                className="rounded-lg p-2 bg-accent-hover text-text-primary hover:bg-accent-hover/70 transition-colors"
              >
                <Settings className="h-4 w-4" />
              </button>
              <button
                onClick={terminateVoiceSession}
                aria-label="Leave voice channel"
                className="flex items-center gap-1.5 rounded-lg bg-text-primary px-3 py-2 text-xs font-semibold text-app-bg transition-colors hover:opacity-90"
              >
                <PhoneOff className="h-4 w-4" />
                Leave
              </button>
            </div>
          </div>

          {/* Voice Settings & Diagnostics Dropdown */}
          {voiceSettingsOpen && (
            <div className="border-t border-border-color pt-3 flex flex-col gap-3 min-w-[300px]">
              {/* Microphone Selection */}
              <div className="flex flex-col gap-1">
                <label className="text-[10px] font-bold uppercase tracking-wider text-text-secondary">Microphone</label>
                <div className="relative">
                  <select
                    value={selectedAudioInputId ?? ''}
                    onChange={(e) => setAudioInput(e.target.value)}
                    className="w-full appearance-none rounded-lg border border-border-color bg-input-bg px-3 py-1.5 pr-8 text-xs text-text-primary outline-none focus:border-accent"
                  >
                    <option value="">System Default</option>
                    {audioInputs.map((d) => (
                      <option key={d.deviceId} value={d.deviceId}>
                        {d.label || `Microphone (${d.deviceId.slice(0, 8)}…)`}
                      </option>
                    ))}
                  </select>
                  <ChevronDown className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-text-secondary" />
                </div>
              </div>

              {/* Microphone Test */}
              <div className="flex flex-col gap-1.5">
                <button
                  onClick={micTestActive ? stopMicTest : startMicTest}
                  className={`flex items-center justify-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-semibold transition-colors ${
                    micTestActive
                      ? 'border-accent bg-accent-wash text-accent-text'
                      : 'border-border-color bg-accent-hover text-text-primary hover:bg-accent-hover/70'
                  }`}
                >
                  <Mic className="h-3.5 w-3.5" />
                  {micTestActive ? 'Stop Test' : 'Test Microphone'}
                </button>
                {micTestActive && (
                  <div className="h-2 w-full rounded-full bg-accent-hover overflow-hidden">
                    <div
                      className="h-full bg-accent transition-all duration-75"
                      style={{ width: `${Math.round(micLevel * 100)}%` }}
                    />
                  </div>
                )}
              </div>

              {/* Connection Diagnostics */}
              <div className="flex flex-col gap-1.5">
                <button
                  onClick={runConnectionDiag}
                  disabled={diagRunning}
                  className="flex items-center justify-center gap-1.5 rounded-lg border border-border-color bg-accent-hover px-3 py-1.5 text-xs font-semibold text-text-primary transition-colors hover:bg-accent-hover/70 disabled:opacity-50"
                >
                  {diagRunning ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Activity className="h-3.5 w-3.5" />}
                  {diagRunning ? 'Running…' : 'Test Connection'}
                </button>
                {diagResults.length > 0 && (
                  <div className="flex flex-col gap-1 text-[10px] font-mono">
                    {diagResults.map((r) => (
                      <div key={r.step} className="flex items-center gap-1.5">
                        {r.status === 'pending' && <Loader2 className="h-3 w-3 animate-spin text-text-secondary" />}
                        {r.status === 'ok' && <Check className="h-3 w-3 text-accent" />}
                        {r.status === 'fail' && <PhoneOff className="h-3 w-3 text-text-primary" />}
                        <span className={r.status === 'fail' ? 'text-text-primary' : 'text-text-secondary'}>
                          {r.step}{r.detail ? ` — ${r.detail}` : ''}
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      )}

      {/* Sovereignty Manifesto Overlay / modal */}
      {manifestoOpen && (
        <div className="absolute inset-0 z-50 bg-app-bg/95 flex items-center justify-center p-6 transition-opacity duration-300">
          <div className="w-full max-w-2xl border border-border-color bg-panel-bg rounded-xl p-6 flex flex-col gap-6 overflow-y-auto max-h-[85vh]">
            <div className="flex justify-between items-center border-b border-border-color pb-2">
              <h2 className="text-lg font-bold tracking-wide">Sovereignty Manifesto</h2>
              <button
                onClick={() => setManifestoOpen(false)}
                className="rounded-lg border border-border-color bg-app-bg px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:bg-accent-hover transition-colors"
              >
                Close [Esc]
              </button>
            </div>

            <div className="flex flex-col gap-4 text-sm text-text-secondary">
              <div>
                <h3 className="text-base font-semibold text-text-primary mb-1">The Digital Enclosure Problem</h3>
                <p className="leading-relaxed">
                  Modern platforms extract sovereignty from communities. Hosting code, data, books, and communication inside centralized silos builds an enclosure of digital serfdom. Telos rejects this condition.
                </p>
              </div>

              <div>
                <h3 className="text-base font-semibold text-text-primary mb-1">The Telos Design Creed</h3>
                <p className="leading-relaxed">
                  To remain sovereign, code must be deliberate, not decorative. Design must be flat, grayscale, and carry state through structure rather than semantic hues.
                </p>
              </div>

              <div className="mt-2">
                <h4 className="text-xs font-bold uppercase tracking-wider text-text-primary mb-2">docker-compose.yml Excerpt</h4>
                <pre className="p-3 bg-app-bg border border-border-color rounded-lg font-mono text-xs text-text-secondary overflow-x-auto">
{`services:
  traefik:
    ports:
      - "127.0.0.1:443:443"   # loopback-only bind

  telos-core:
    build:
      context: ./backend
    environment:
      - DATABASE_URL=postgres://\${POSTGRES_USER}:\${POSTGRES_PASSWORD}@postgres:5432/\${POSTGRES_DB}`}
                </pre>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
