--
-- PostgreSQL database dump
--

\restrict vYhk1QhB9wPPtudmC4h0mrPkdejKagY6PjNZMfuKhhDhM2SpJ1MSqiGBZq9jxEp

-- Dumped from database version 16.14
-- Dumped by pg_dump version 16.14

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: channels; Type: TABLE; Schema: public; Owner: telos
--

CREATE TABLE public.channels (
    id character varying(50) NOT NULL,
    name character varying(50) NOT NULL,
    type character varying(10) DEFAULT 'text'::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE public.channels OWNER TO telos;

--
-- Name: messages; Type: TABLE; Schema: public; Owner: telos
--

CREATE TABLE public.messages (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    channel_id character varying(50),
    user_id character varying(50),
    content text NOT NULL,
    "timestamp" timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE public.messages OWNER TO telos;

--
-- Name: schema_migrations; Type: TABLE; Schema: public; Owner: telos
--

CREATE TABLE public.schema_migrations (
    version integer NOT NULL,
    applied_at timestamp with time zone DEFAULT now()
);


ALTER TABLE public.schema_migrations OWNER TO telos;

--
-- Name: users; Type: TABLE; Schema: public; Owner: telos
--

CREATE TABLE public.users (
    id character varying(50) NOT NULL,
    username character varying(50) NOT NULL,
    avatar character varying(10) NOT NULL,
    role character varying(20) DEFAULT 'Member'::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE public.users OWNER TO telos;

--
-- Data for Name: channels; Type: TABLE DATA; Schema: public; Owner: telos
--

COPY public.channels (id, name, type, created_at) FROM stdin;
general	general	text	2026-07-09 04:12:34.065839+00
dev-chat	dev-chat	text	2026-07-09 04:12:34.065839+00
catalog-updates	catalog-updates	text	2026-07-09 04:12:34.065839+00
voice-lounge	voice-lounge	voice	2026-07-09 04:12:34.065839+00
gaming-lounge	gaming-lounge	voice	2026-07-09 04:12:34.065839+00
\.


--
-- Data for Name: messages; Type: TABLE DATA; Schema: public; Owner: telos
--

COPY public.messages (id, channel_id, user_id, content, "timestamp") FROM stdin;
233caee2-1c7b-46a4-b1b0-bef42e764c71	general	cleadmon	Live container debug message	2026-07-11 04:47:38.587819+00
2b850670-a2a6-4199-a413-1325d896da21	general	cleadmon	Live container debug message	2026-07-11 04:48:00.491399+00
93614b3d-2368-41b6-bd27-57e7ef40e22a	general	cleadmon	Live container debug message	2026-07-11 04:48:10.464104+00
aa7d0de7-2f62-4a09-95cb-acf34cb7a629	general	cleadmon	hello	2026-07-11 04:52:10.386434+00
2ef32e46-b7d2-4ec9-8c40-3d9aa162c2f5	general	cleadmon	test	2026-07-11 04:55:54.293957+00
11fd6ecb-2066-4d59-8c35-f2dd4beb77bd	dev-chat	cleadmon	test	2026-07-11 04:55:58.337694+00
a212a57c-8313-4864-81b8-7bbcdd10265b	catalog-updates	cleadmon	Hello	2026-07-11 04:56:04.504144+00
6a6e6d4f-8db5-4b4c-84c9-bdde11567d5c	general	cleadmon	Hello	2026-07-11 04:59:14.553112+00
090a6cd0-7eff-45be-b4c2-3882d6137b76	general	cleadmon	Hello, this is an automated UI verification test!	2026-07-11 05:02:50.23098+00
e3e7ff03-ec19-4310-b448-1529d9350f4c	general	cleadmon	Hello, this is an automated UI verification test!	2026-07-11 05:03:04.325207+00
4bcf3699-c438-4b75-9fcb-9d5c1b21c45a	general	cleadmon	Hello, this is an automated UI verification test!	2026-07-11 05:03:31.206161+00
c2c32ed3-e4bc-4938-8565-e00657940a4a	general	cleadmon	Hello, this is an automated UI verification test!	2026-07-11 05:04:19.680748+00
76b4d334-caa9-4191-9ad6-c298c1c8f110	general	cleadmon	Hello, this is an automated UI verification test!	2026-07-11 05:04:34.290681+00
638c1fe0-ab87-4676-abdd-b929b2e11247	dev-chat	cleadmon	Testin	2026-07-11 05:06:10.87226+00
230fc3d9-543d-494a-92e2-5d5d87b99b5a	catalog-updates	cleadmon	hello	2026-07-11 18:29:05.465516+00
\.


--
-- Data for Name: schema_migrations; Type: TABLE DATA; Schema: public; Owner: telos
--

COPY public.schema_migrations (version, applied_at) FROM stdin;
\.


--
-- Data for Name: users; Type: TABLE DATA; Schema: public; Owner: telos
--

COPY public.users (id, username, avatar, role, created_at) FROM stdin;
cleadmon	cleadmon	CL	Host	2026-07-09 04:12:34.065839+00
oracle	oracle	AI	Admin	2026-07-09 04:12:34.065839+00
telos-bot	telos-bot	TB	Admin	2026-07-09 04:12:34.065839+00
\.


--
-- Name: channels channels_name_key; Type: CONSTRAINT; Schema: public; Owner: telos
--

ALTER TABLE ONLY public.channels
    ADD CONSTRAINT channels_name_key UNIQUE (name);


--
-- Name: channels channels_pkey; Type: CONSTRAINT; Schema: public; Owner: telos
--

ALTER TABLE ONLY public.channels
    ADD CONSTRAINT channels_pkey PRIMARY KEY (id);


--
-- Name: messages messages_pkey; Type: CONSTRAINT; Schema: public; Owner: telos
--

ALTER TABLE ONLY public.messages
    ADD CONSTRAINT messages_pkey PRIMARY KEY (id);


--
-- Name: schema_migrations schema_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: telos
--

ALTER TABLE ONLY public.schema_migrations
    ADD CONSTRAINT schema_migrations_pkey PRIMARY KEY (version);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: telos
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: users users_username_key; Type: CONSTRAINT; Schema: public; Owner: telos
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_username_key UNIQUE (username);


--
-- Name: messages messages_channel_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: telos
--

ALTER TABLE ONLY public.messages
    ADD CONSTRAINT messages_channel_id_fkey FOREIGN KEY (channel_id) REFERENCES public.channels(id) ON DELETE CASCADE;


--
-- Name: messages messages_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: telos
--

ALTER TABLE ONLY public.messages
    ADD CONSTRAINT messages_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

\unrestrict vYhk1QhB9wPPtudmC4h0mrPkdejKagY6PjNZMfuKhhDhM2SpJ1MSqiGBZq9jxEp

