#!/usr/bin/env python3
"""Create an independent Windows copy of ChatGPT with Codex multiplexing.

The Microsoft Store installation is never modified. The copied application has
a small DOM bridge, a narrowly scoped Electron bridge for the official browser
sign-in hand-off, and version-pinned renderer patches for profile, Plugins, and
rate-limit reset account selection. Every renderer anchor is matched exactly
once so a Store update fails closed instead of applying a possibly incorrect
rewrite.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import secrets
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
VERSION = (ROOT / "VERSION").read_text(encoding="utf-8").strip()
APP_FAMILY_PREFIX = "OpenAI.Codex_"
CONTROL_PORT = 48123
# Codex Relay is the public product name. Keep the old name only as a
# migration source: existing 0.2.x installations, their browser profile, and
# their update helper must remain usable while the replacement is staged.
ROUTER_APP_NAME = "Codex Relay"
LEGACY_ROUTER_APP_NAME = "Codex Subscription Router"
ROUTER_APP_DIRECTORY = "app"
ASAR_UNPACK_DIRECTORIES = "{node_modules/better-sqlite3,node_modules/node-pty}"
ASAR_UNPACK_FILES = "{node_modules/@worklouder/device-kit-oai/node_modules/@serialport/bindings-cpp/build/Release/bindings.node,node_modules/@worklouder/device-kit-oai/node_modules/node-hid/build/Release/HID.node}"
# These legacy anchors are deliberately tied to the matching ASAR hash in
# WINDOWS_RENDERER_PROFILES below. The Windows bundle does not share minified
# symbol names with the macOS build, so keeping them here makes a new Store
# version an explicit review/update step.
PROFILE_QUERY_ANCHOR = "async function yol(){let e=await r_.safeGet(`/wham/profiles/me`);"
PROFILE_QUERY_REPLACEMENT = (
    "async function yol(){let e=await(globalThis.CodexMuxWindows?.profileData?.()"
    "??r_.safeGet(`/wham/profiles/me`));"
)
USAGE_STATUS_ANCHOR = "try{return await r_.safeGet(`/wham/usage`)}catch(e){"
USAGE_STATUS_REPLACEMENT = (
    "try{return typeof globalThis.CodexMuxWindows?.usageStatus===`function`?"
    "await globalThis.CodexMuxWindows.usageStatus():{}}catch(e){"
)
PLUGIN_REQUEST_ANCHOR = (
    "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw Error("
    "`AppServerRequestClient is missing a message dispatcher`);return e===`config/read`?"
    "this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,e===`plugin/list`&&n?.timeoutMs==null?"
    "{...n,timeoutMs:Osn}:n)}"
)
PLUGIN_REQUEST_REPLACEMENT = (
    "async sendRequest(e,t,n){t=globalThis.CodexMuxWindows?.scopePluginRequest?.(e,t)??t;"
    "if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);"
    "return e===`config/read`?this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,"
    "e===`plugin/list`&&n?.timeoutMs==null?{...n,timeoutMs:Osn}:n)}"
)
RESET_QUERY_ANCHOR = (
    "function Coi(){let e=(0,SI.c)(1),t;return e[0]===Symbol.for(`react.memo_cache_sentinel`)?"
    "(t={queryKey:[`rate-limit-reset-credits`],queryFn:woi,refetchInterval:Gp.ONE_MINUTE,"
    "staleTime:Gp.FIVE_SECONDS},e[0]=t):t=e[0],Lt(t)}"
)
RESET_QUERY_REPLACEMENT = (
    "function Coi(){let e=(0,n$s.useSyncExternalStore)("
    "e=>globalThis.CodexMuxWindows?.subscribeReset?.(e)??(()=>{}),"
    "()=>globalThis.CodexMuxWindows?.getResetAccountId?.()??null,()=>null),"
    "t={queryKey:[`rate-limit-reset-credits`,e??`primary`],"
    "queryFn:e?()=>globalThis.CodexMuxWindows.rateLimitResets(e):woi,"
    "refetchInterval:Gp.ONE_MINUTE,staleTime:Gp.FIVE_SECONDS};return Lt(t)}"
)
RESET_MUTATION_ANCHOR = (
    "function Toi(){let e=(0,SI.c)(3),t=lt(),n=Fw(),r;return e[0]!==n||e[1]!==t?"
    "(r={mutationFn:Eoi,onSuccess:(e,r)=>{let{creditId:i}=r,a=e.code;"
    "if(a===`reset`||a===`already_redeemed`){let n=e.code===`reset`?e.credit?.id??i:i;"
    "t.setQueryData([`rate-limit-reset-credits`],e=>Yai(e,a,n))}"
    "Promise.all([n([`rate-limit-status`]),n([`rate-limit-reset-credits`])])}},"
    "e[0]=n,e[1]=t,e[2]=r):r=e[2],$t(r)}"
)
RESET_MUTATION_REPLACEMENT = (
    "function Toi(){let e=lt(),t=Fw(),n=globalThis.CodexMuxWindows?.getResetAccountId?.()??null,"
    "r=[`rate-limit-reset-credits`,n??`primary`];return $t({"
    "mutationFn:n?i=>globalThis.CodexMuxWindows.consumeRateLimitReset(n,i):Eoi,"
    "onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){"
    "let t=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>Yai(e,o,t))}"
    "Promise.all([t([`rate-limit-status`]),t(r)])}})}"
)
SELECTED_USAGE_ANCHOR = "let y=v;if(g!=null){"
SELECTED_USAGE_REPLACEMENT = (
    "let y=globalThis.CodexMuxWindows?.selectedResetUsageWindows?.()??v;if(g!=null){"
)
RESET_HEADER_ANCHOR = (
    "let ve;t[46]===ge?ve=t[47]:(ve=(0,L0.jsxs)(QL,{children:[ge,_e]}),"
    "t[46]=ge,t[47]=ve);"
)
RESET_HEADER_REPLACEMENT = (
    "let ve=(0,L0.jsxs)(QL,{children:[ge,_e,"
    "(0,L0.jsx)(`codex-mux-reset-picker`,{})]});"
)
PROFILE_PICKER_ANCHOR = "children:[Yt,Xt,xn,Sn]"
PROFILE_PICKER_REPLACEMENT = "children:[(0,$.jsx)(`codex-mux-profile-picker`,{}),Yt,Xt,xn,Sn]"
PLUGIN_PICKER_ANCHOR = "I=(0,D.jsx)(g,{title:O,subtitle:k,action:F,children:w})"
PLUGIN_PICKER_REPLACEMENT = (
    "I=(0,D.jsx)(g,{title:O,subtitle:k,action:F,children:(0,D.jsxs)(`div`,"
    "{className:`contents`,children:[(0,D.jsx)(`codex-mux-plugin-picker`,{}),w]})})"
)

# The current Windows Store release renamed its minified symbols and changed
# the reset modal component. Keep each renderer rewrite tied to the exact ASAR
# hash so older checked builds continue to work while an unfamiliar release
# still fails closed.
CURRENT_RENDERER_PROFILE = {
    "profile_query_anchor": "async function $Hl(){let e=await K_.safeGet(`/wham/profiles/me`);",
    "profile_query_replacement": (
        "async function $Hl(){let e=await(globalThis.CodexMuxWindows?.profileData?.()"
        "??K_.safeGet(`/wham/profiles/me`));"
    ),
    "usage_status_anchor": (
        "let e=await K_.safeGet(`/wham/usage`,{additionalHeaders:{\"OAI-App-Brand\":H_}})"
    ),
    "usage_status_replacement": (
        "let e=typeof globalThis.CodexMuxWindows?.usageStatus===`function`?"
        "await globalThis.CodexMuxWindows.usageStatus():{}"
    ),
    "plugin_request_anchor": (
        "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw Error("
        "`AppServerRequestClient is missing a message dispatcher`);return e===`config/read`?"
        "this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,e===`plugin/list`&&n?.timeoutMs==null?"
        "{...n,timeoutMs:fFt}:n)}"
    ),
    "plugin_request_replacement": (
        "async sendRequest(e,t,n){t=globalThis.CodexMuxWindows?.scopePluginRequest?.(e,t)??t;"
        "if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);"
        "return e===`config/read`?this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,"
        "e===`plugin/list`&&n?.timeoutMs==null?{...n,timeoutMs:fFt}:n)}"
    ),
    "reset_query_anchor": (
        "function Oxa(){let e=(0,JV.c)(1),t;return e[0]===Symbol.for(`react.memo_cache_sentinel`)?"
        "(t={queryKey:[`rate-limit-reset-credits`],queryFn:kxa,refetchInterval:Mp.ONE_MINUTE,"
        "staleTime:Mp.FIVE_SECONDS},e[0]=t):t=e[0],It(t)}"
    ),
    "reset_query_replacement": (
        # In the 26.818 Windows renderer this module's React namespace is
        # `rxa`; `n$s` is an MCP schema declared elsewhere in app-initial.
        # Keeping the alias tied to this exact anchor prevents the Usage page
        # from crashing while the reset-account picker subscribes to state.
        "function Oxa(){let e=(0,rxa.useSyncExternalStore)("
        "e=>globalThis.CodexMuxWindows?.subscribeReset?.(e)??(()=>{}),"
        "()=>globalThis.CodexMuxWindows?.getResetAccountId?.()??null,()=>null),"
        "t={queryKey:[`rate-limit-reset-credits`,e??`primary`],"
        "queryFn:e?()=>globalThis.CodexMuxWindows.rateLimitResets(e):kxa,"
        "refetchInterval:Mp.ONE_MINUTE,staleTime:Mp.FIVE_SECONDS};return It(t)}"
    ),
    "reset_mutation_anchor": (
        "function Axa(){let e=(0,JV.c)(3),t=ct(),n=wb(),r;return e[0]!==n||e[1]!==t?"
        "(r={mutationFn:jxa,onSuccess:(e,r)=>{let{creditId:i}=r,a=e.code;"
        "if(a===`reset`||a===`already_redeemed`){let n=e.code===`reset`?e.credit?.id??i:i;"
        "t.setQueryData([`rate-limit-reset-credits`],e=>exa(e,a,n))}"
        "Promise.all([n([`rate-limit-status`]),n([`rate-limit-reset-credits`])])}},"
        "e[0]=n,e[1]=t,e[2]=r):r=e[2],Qt(r)}"
    ),
    "reset_mutation_replacement": (
        "function Axa(){let e=ct(),t=wb(),n=globalThis.CodexMuxWindows?.getResetAccountId?.()??null,"
        "r=[`rate-limit-reset-credits`,n??`primary`];return Qt({"
        "mutationFn:n?i=>globalThis.CodexMuxWindows.consumeRateLimitReset(n,i):jxa,"
        "onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){"
        "let s=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>exa(e,o,s))}"
        "Promise.all([t([`rate-limit-status`]),t(r)])}})}"
    ),
    "selected_usage_anchor": "let y=v;if(g!=null){",
    "selected_usage_replacement": (
        "let y=globalThis.CodexMuxWindows?.selectedResetUsageWindows?.()??v;if(g!=null){"
    ),
    "reset_header_anchor": (
        "let _e;t[46]===he?_e=t[47]:(_e=(0,u4.jsxs)(tz,{children:[he,ge]}),"
        "t[46]=he,t[47]=_e);"
    ),
    "reset_header_replacement": (
        "let _e=(0,u4.jsxs)(tz,{children:[he,ge,(0,u4.jsx)(`codex-mux-reset-picker`,{})]});"
    ),
    "profile_picker_anchor": "children:[Sn,Cn,Tn]",
    "profile_picker_replacement": "children:[(0,$.jsx)(`codex-mux-profile-picker`,{}),Sn,Cn,Tn]",
    "plugin_picker_anchor": "I=(0,D.jsx)(v,{title:O,subtitle:k,action:F,children:w})",
    "plugin_picker_replacement": (
        "I=(0,D.jsx)(v,{title:O,subtitle:k,action:F,children:(0,D.jsxs)(`div`,"
        "{className:`contents`,children:[(0,D.jsx)(`codex-mux-plugin-picker`,{}),w]})})"
    ),
}

# Codex 26.818.3698.0 (Windows Store). This release keeps the same renderer
# surfaces but rolled the minifier aliases forward. Keep it as a separate,
# hash-pinned profile so an unrelated Store update still fails closed instead
# of applying a renderer patch at an ambiguous location.
STORE_26_818_3698_RENDERER_PROFILE = {
    "profile_query_anchor": "async function iWl(){let e=await V_.safeGet(`/wham/profiles/me`);",
    "profile_query_replacement": (
        "async function iWl(){let e=await(globalThis.CodexMuxWindows?.profileData?.()"
        "??V_.safeGet(`/wham/profiles/me`));"
    ),
    "usage_status_anchor": (
        "let e=await V_.safeGet(`/wham/usage`,{additionalHeaders:{\"OAI-App-Brand\":L_}})"
    ),
    "usage_status_replacement": (
        "let e=typeof globalThis.CodexMuxWindows?.usageStatus===`function`?"
        "await globalThis.CodexMuxWindows.usageStatus():{}"
    ),
    "plugin_request_anchor": (
        "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw Error("
        "`AppServerRequestClient is missing a message dispatcher`);return e===`config/read`?"
        "this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,e===`plugin/list`&&n?.timeoutMs==null?"
        "{...n,timeoutMs:_Ft}:n)}"
    ),
    "plugin_request_replacement": (
        "async sendRequest(e,t,n){t=globalThis.CodexMuxWindows?.scopePluginRequest?.(e,t)??t;"
        "if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);"
        "return e===`config/read`?this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,"
        "e===`plugin/list`&&n?.timeoutMs==null?{...n,timeoutMs:_Ft}:n)}"
    ),
    "reset_query_anchor": (
        "function Zxa(){let e=(0,kV.c)(1),t;return e[0]===Symbol.for(`react.memo_cache_sentinel`)?"
        "(t={queryKey:[`rate-limit-reset-credits`],queryFn:Qxa,refetchInterval:Op.ONE_MINUTE,"
        "staleTime:Op.FIVE_SECONDS},e[0]=t):t=e[0],It(t)}"
    ),
    "reset_query_replacement": (
        "function Zxa(){let e=(0,Exa.useSyncExternalStore)("
        "e=>globalThis.CodexMuxWindows?.subscribeReset?.(e)??(()=>{}),"
        "()=>globalThis.CodexMuxWindows?.getResetAccountId?.()??null,()=>null),"
        "t={queryKey:[`rate-limit-reset-credits`,e??`primary`],"
        "queryFn:e?()=>globalThis.CodexMuxWindows.rateLimitResets(e):Qxa,"
        "refetchInterval:Op.ONE_MINUTE,staleTime:Op.FIVE_SECONDS};return It(t)}"
    ),
    "reset_mutation_anchor": (
        "function $xa(){let e=(0,kV.c)(3),t=ct(),n=Cb(),r;return e[0]!==n||e[1]!==t?"
        "(r={mutationFn:eSa,onSuccess:(e,r)=>{let{creditId:i}=r,a=e.code;"
        "if(a===`reset`||a===`already_redeemed`){let n=e.code===`reset`?e.credit?.id??i:i;"
        "t.setQueryData([`rate-limit-reset-credits`],e=>Cxa(e,a,n))}"
        "Promise.all([n([`rate-limit-status`]),n([`rate-limit-reset-credits`])])}},"
        "e[0]=n,e[1]=t,e[2]=r):r=e[2],Qt(r)}"
    ),
    "reset_mutation_replacement": (
        "function $xa(){let e=ct(),t=Cb(),n=globalThis.CodexMuxWindows?.getResetAccountId?.()??null,"
        "r=[`rate-limit-reset-credits`,n??`primary`];return Qt({"
        "mutationFn:n?i=>globalThis.CodexMuxWindows.consumeRateLimitReset(n,i):eSa,"
        "onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){"
        "let s=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>Cxa(e,o,s))}"
        "Promise.all([t([`rate-limit-status`]),t(r)])}})}"
    ),
    "selected_usage_anchor": "let y=v;if(g!=null){",
    "selected_usage_replacement": (
        "let y=globalThis.CodexMuxWindows?.selectedResetUsageWindows?.()??v;if(g!=null){"
    ),
    "reset_header_anchor": (
        "let _e;t[46]===he?_e=t[47]:(_e=(0,u4.jsxs)(IR,{children:[he,ge]}),"
        "t[46]=he,t[47]=_e);"
    ),
    "reset_header_replacement": (
        "let _e=(0,u4.jsxs)(IR,{children:[he,ge,"
        "(0,u4.jsx)(`codex-mux-reset-picker`,{})]});"
    ),
    "profile_picker_anchor": "children:[Sn,Cn,Tn]",
    "profile_picker_replacement": "children:[(0,$.jsx)(`codex-mux-profile-picker`,{}),Sn,Cn,Tn]",
    "plugin_picker_anchor": "I=(0,D.jsx)(v,{title:O,subtitle:k,action:F,children:w})",
    "plugin_picker_replacement": (
        "I=(0,D.jsx)(v,{title:O,subtitle:k,action:F,children:(0,D.jsxs)(`div`,"
        "{className:`contents`,children:[(0,D.jsx)(`codex-mux-plugin-picker`,{}),w]})})"
    ),
}

# Codex 26.818.4152.0 (Windows Store). This build keeps the same feature
# surfaces but rolled the app-initial module aliases forward again. The native
# Usage & billing page is still the source of layout and billing copy; its
# account-scoped requests are redirected through the Relay bridge so the page
# cannot accidentally read the official Codex account.
STORE_26_818_4152_RENDERER_PROFILE = {
    "profile_query_anchor": "async function iWl(){let e=await F_.safeGet(`/wham/profiles/me`);",
    "profile_query_replacement": (
        "async function iWl(){let e=await(globalThis.CodexMuxWindows?.profileData?.()"
        "??F_.safeGet(`/wham/profiles/me`));"
    ),
    "usage_status_anchor": (
        "let e=await F_.safeGet(`/wham/usage`,{additionalHeaders:{\"OAI-App-Brand\":j_}})"
    ),
    "usage_status_replacement": (
        "let e=typeof globalThis.CodexMuxWindows?.usageStatus===`function`?"
        "await globalThis.CodexMuxWindows.usageStatus():{}"
    ),
    "plugin_request_anchor": (
        "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw Error("
        "`AppServerRequestClient is missing a message dispatcher`);return e===`config/read`?"
        "this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,e===`plugin/list`&&n?.timeoutMs==null?"
        "{...n,timeoutMs:CFt}:n)}"
    ),
    "plugin_request_replacement": (
        "async sendRequest(e,t,n){t=globalThis.CodexMuxWindows?.scopePluginRequest?.(e,t)??t;"
        "if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);"
        "return e===`config/read`?this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,"
        "e===`plugin/list`&&n?.timeoutMs==null?{...n,timeoutMs:CFt}:n)}"
    ),
    "reset_query_anchor": (
        "function eSa(){let e=(0,EV.c)(1),t;return e[0]===Symbol.for(`react.memo_cache_sentinel`)?"
        "(t={queryKey:[`rate-limit-reset-credits`],queryFn:tSa,refetchInterval:Ap.ONE_MINUTE,"
        "staleTime:Ap.FIVE_SECONDS},e[0]=t):t=e[0],It(t)}"
    ),
    "reset_query_replacement": (
        "function eSa(){let n=(0,u4.useSyncExternalStore)("
        "e=>globalThis.CodexMuxWindows?.subscribeReset?.(e)??(()=>{}),"
        "()=>globalThis.CodexMuxWindows?.getResetAccountId?.()??null,()=>null);return It({"
        "queryKey:[`rate-limit-reset-credits`,n??`primary`],"
        "queryFn:()=>globalThis.CodexMuxWindows?.rateLimitResets?.(n??`primary`)??tSa(),"
        "refetchInterval:Ap.ONE_MINUTE,staleTime:Ap.FIVE_SECONDS})}"
    ),
    "reset_mutation_anchor": (
        "function nSa(){let e=(0,EV.c)(3),t=ct(),n=mb(),r;return e[0]!==n||e[1]!==t?"
        "(r={mutationFn:rSa,onSuccess:(e,r)=>{let{creditId:i}=r,a=e.code;"
        "if(a===`reset`||a===`already_redeemed`){let n=e.code===`reset`?e.credit?.id??i:i;"
        "t.setQueryData([`rate-limit-reset-credits`],e=>Exa(e,a,n))}"
        "Promise.all([n([`rate-limit-status`]),n([`rate-limit-reset-credits`])])}},"
        "e[0]=n,e[1]=t,e[2]=r):r=e[2],Qt(r)}"
    ),
    "reset_mutation_replacement": (
        "function nSa(){let e=ct(),t=mb(),n=globalThis.CodexMuxWindows?.getResetAccountId?.()??null,"
        "r=[`rate-limit-reset-credits`,n??`primary`];return Qt({"
        "mutationFn:n?i=>globalThis.CodexMuxWindows.consumeRateLimitReset(n,i):rSa,"
        "onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){"
        "let s=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>Exa(e,o,s))}"
        "Promise.all([t([`rate-limit-status`]),t(r)])}})}"
    ),
    "selected_usage_anchor": "let y=v;if(g!=null){",
    "selected_usage_replacement": (
        "let y=globalThis.CodexMuxWindows?.selectedResetUsageWindows?.()??v;if(g!=null){"
    ),
    "reset_header_anchor": (
        "let _e;t[46]===he?_e=t[47]:(_e=(0,u4.jsxs)(OR,{children:[he,ge]}),"
        "t[46]=he,t[47]=_e);"
    ),
    "reset_header_replacement": (
        "let _e=(0,u4.jsxs)(OR,{children:[he,ge,(0,u4.jsx)(`codex-mux-reset-picker`,{})]});"
    ),
    "profile_picker_anchor": "children:[Sn,Cn,Tn]",
    "profile_picker_replacement": "children:[(0,$.jsx)(`codex-mux-profile-picker`,{}),Sn,Cn,Tn]",
    "plugin_picker_anchor": "I=(0,D.jsx)(v,{title:O,subtitle:k,action:F,children:w})",
    "plugin_picker_replacement": (
        "I=(0,D.jsx)(v,{title:O,subtitle:k,action:F,children:(0,D.jsxs)(`div`,"
        "{className:`contents`,children:[(0,D.jsx)(`codex-mux-plugin-picker`,{}),w]})})"
    ),
}

# Codex 26.818.5229.0 (Windows Store). The renderer still exposes the reviewed
# Relay surfaces, but the Profile bundle now reuses its short children shape in
# unrelated cards and the Plugins row moved into a neighboring asset. This
# profile records the exact unique component anchors validated against ASAR
# c5d839bc...0479; structural discovery is only a review aid and is never used
# automatically for an installed release.
STORE_26_818_5229_RENDERER_PROFILE = {
    **STORE_26_818_4152_RENDERER_PROFILE,
    "profile_query_anchor": "async function lGl(){let e=await z_.safeGet(`/wham/profiles/me`);",
    "profile_query_replacement": (
        "async function lGl(){let e=await(globalThis.CodexMuxWindows?.profileData?.()"
        "??z_.safeGet(`/wham/profiles/me`));"
    ),
    "usage_status_anchor": (
        "let e=await z_.safeGet(`/wham/usage`,{additionalHeaders:{\"OAI-App-Brand\":F_}})"
    ),
    "plugin_request_anchor": (
        "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw Error("
        "`AppServerRequestClient is missing a message dispatcher`);return e===`config/read`?"
        "this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,e===`plugin/list`&&n?.timeoutMs==null?"
        "{...n,timeoutMs:TFt}:n)}"
    ),
    "plugin_request_replacement": (
        "async sendRequest(e,t,n){t=globalThis.CodexMuxWindows?.scopePluginRequest?.(e,t)??t;"
        "if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);"
        "return e===`config/read`?this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,"
        "e===`plugin/list`&&n?.timeoutMs==null?{...n,timeoutMs:TFt}:n)}"
    ),
    "reset_query_anchor": (
        "function vCa(){let e=(0,PV.c)(1),t;return e[0]===Symbol.for(`react.memo_cache_sentinel`)?"
        "(t={queryKey:[`rate-limit-reset-credits`],queryFn:yCa,refetchInterval:jp.ONE_MINUTE,"
        "staleTime:jp.FIVE_SECONDS},e[0]=t):t=e[0],It(t)}"
    ),
    "reset_query_replacement": (
        "function vCa(){let n=(0,f0.useSyncExternalStore)("
        "e=>globalThis.CodexMuxWindows?.subscribeReset?.(e)??(()=>{}),"
        "()=>globalThis.CodexMuxWindows?.getResetAccountId?.()??null,()=>null);return It({"
        "queryKey:[`rate-limit-reset-credits`,n??`primary`],"
        "queryFn:()=>globalThis.CodexMuxWindows?.rateLimitResets?.(n??`primary`)??yCa(),"
        "refetchInterval:jp.ONE_MINUTE,staleTime:jp.FIVE_SECONDS})}"
    ),
    "reset_mutation_anchor": (
        "function bCa(){let e=(0,PV.c)(3),t=ct(),n=_b(),r;return e[0]!==n||e[1]!==t?"
        "(r={mutationFn:xCa,onSuccess:(e,r)=>{let{creditId:i}=r,a=e.code;"
        "if(a===`reset`||a===`already_redeemed`){let n=e.code===`reset`?e.credit?.id??i:i;"
        "t.setQueryData([`rate-limit-reset-credits`],e=>WSa(e,a,n))}"
        "Promise.all([n([`rate-limit-status`]),n([`rate-limit-reset-credits`])])}},"
        "e[0]=n,e[1]=t,e[2]=r):r=e[2],Qt(r)}"
    ),
    "reset_mutation_replacement": (
        "function bCa(){let e=ct(),t=_b(),n=globalThis.CodexMuxWindows?.getResetAccountId?.()??null,"
        "r=[`rate-limit-reset-credits`,n??`primary`];return Qt({"
        "mutationFn:n?i=>globalThis.CodexMuxWindows.consumeRateLimitReset(n,i):xCa,"
        "onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){"
        "let s=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>WSa(e,o,s))}"
        "Promise.all([t([`rate-limit-status`]),t(r)])}})}"
    ),
    "reset_header_anchor": (
        "let _e;t[46]===he?_e=t[47]:(_e=(0,f0.jsxs)(BR,{children:[he,ge]}),"
        "t[46]=he,t[47]=_e);"
    ),
    "reset_header_replacement": (
        "let _e=(0,f0.jsxs)(BR,{children:[he,ge,(0,f0.jsx)(`codex-mux-reset-picker`,{})]});"
    ),
    "profile_picker_anchor": "En=(0,$.jsxs)(Ot,{ref:Ce,header:Gt,children:[Sn,Cn,Tn]})",
    "profile_picker_replacement": (
        "En=(0,$.jsxs)(Ot,{ref:Ce,header:Gt,"
        "children:[(0,$.jsx)(`codex-mux-profile-picker`,{}),Sn,Cn,Tn]})"
    ),
    "plugin_picker_anchor": "I=(0,D.jsx)(_,{title:O,subtitle:k,action:F,children:w})",
    "plugin_picker_replacement": (
        "I=(0,D.jsx)(_,{title:O,subtitle:k,action:F,children:(0,D.jsxs)(`div`,"
        "{className:`contents`,children:[(0,D.jsx)(`codex-mux-plugin-picker`,{}),w]})})"
    ),
}

# Codex 26.818.5345.0 (Windows Store). A review of the unpacked upstream ASAR
# found that every Relay-owned renderer anchor and replacement is byte-for-byte
# identical to the 26.818.5229.0 profile. Keep a separately named exact profile
# so the new source hash is explicit and remains covered by release fixtures.
STORE_26_818_5345_RENDERER_PROFILE = {
    **STORE_26_818_5229_RENDERER_PROFILE,
}

# Codex 26.818.8289.0 (Windows Store). The generated app-server schema retains
# the reviewed quota, turn-completion, thread/resume, and Goal contracts, while
# the renderer rolls several minifier aliases. These anchors were structurally
# discovered against the unpacked upstream ASAR and then reviewed as unique
# exact-once patch sites before recording the source hash below.
STORE_26_818_8289_RENDERER_PROFILE = {
    **STORE_26_818_5345_RENDERER_PROFILE,
    "profile_query_anchor": "async function fGl(){let e=await F_.safeGet(`/wham/profiles/me`);",
    "profile_query_replacement": (
        "async function fGl(){let e=await(globalThis.CodexMuxWindows?.profileData?.()??"
        "F_.safeGet(`/wham/profiles/me`));"
    ),
    "usage_status_anchor": (
        "let e=await F_.safeGet(`/wham/usage`,{additionalHeaders:{\"OAI-App-Brand\":j_}})"
    ),
    "plugin_request_anchor": (
        "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw "
        "Error(`AppServerRequestClient is missing a message dispatcher`);return "
        "e===`config/read`?this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,"
        "e===`plugin/list`&&n?.timeoutMs==null?{...n,timeoutMs:kFt}:n)}"
    ),
    "plugin_request_replacement": (
        "async sendRequest(e,t,n){t=globalThis.CodexMuxWindows?.scopePluginRequest?.(e,t)??t;"
        "if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);"
        "return e===`config/read`?this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,"
        "e===`plugin/list`&&n?.timeoutMs==null?{...n,timeoutMs:kFt}:n)}"
    ),
    "reset_query_anchor": (
        "function TCa(){let e=(0,kV.c)(1),t;return "
        "e[0]===Symbol.for(`react.memo_cache_sentinel`)?"
        "(t={queryKey:[`rate-limit-reset-credits`],queryFn:ECa,refetchInterval:Dp.ONE_MINUTE,"
        "staleTime:Dp.FIVE_SECONDS},e[0]=t):t=e[0],It(t)}"
    ),
    "reset_query_replacement": (
        "function TCa(){let n=(0,d0.useSyncExternalStore)"
        "(e=>globalThis.CodexMuxWindows?.subscribeReset?.(e)??(()=>{}),"
        "()=>globalThis.CodexMuxWindows?.getResetAccountId?.()??null,()=>null);"
        "return It({queryKey:[`rate-limit-reset-credits`,n??`primary`],"
        "queryFn:()=>globalThis.CodexMuxWindows?.rateLimitResets?.(n??`primary`)??ECa(),"
        "refetchInterval:Dp.ONE_MINUTE,staleTime:Dp.FIVE_SECONDS})}"
    ),
    "reset_mutation_anchor": (
        "function DCa(){let e=(0,kV.c)(3),t=ct(),n=hb(),r;return e[0]!==n||e[1]!==t?"
        "(r={mutationFn:OCa,onSuccess:(e,r)=>{let{creditId:i}=r,a=e.code;"
        "if(a===`reset`||a===`already_redeemed`){let n=e.code===`reset`?e.credit?.id??i:i;"
        "t.setQueryData([`rate-limit-reset-credits`],e=>ZSa(e,a,n))}"
        "Promise.all([n([`rate-limit-status`]),n([`rate-limit-reset-credits`])])}},"
        "e[0]=n,e[1]=t,e[2]=r):r=e[2],Qt(r)}"
    ),
    "reset_mutation_replacement": (
        "function DCa(){let e=ct(),t=hb(),n=globalThis.CodexMuxWindows?.getResetAccountId?.()??null,"
        "r=[`rate-limit-reset-credits`,n??`primary`];return Qt({"
        "mutationFn:n?i=>globalThis.CodexMuxWindows.consumeRateLimitReset(n,i):OCa,"
        "onSuccess:(n,i)=>{let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){"
        "let s=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>ZSa(e,o,s))}"
        "Promise.all([t([`rate-limit-status`]),t(r)])}})}"
    ),
    "reset_header_anchor": (
        "let _e;t[46]===he?_e=t[47]:(_e=(0,d0.jsxs)(MR,{children:[he,ge]}),"
        "t[46]=he,t[47]=_e);"
    ),
    "reset_header_replacement": (
        "let _e=(0,d0.jsxs)(MR,{children:[he,ge,(0,d0.jsx)(`codex-mux-reset-picker`,{})]});"
    ),
}

# Codex 26.820.7780.0 (Windows Store). The August 27 renderer keeps the
# reviewed Usage & billing, Profile, Plugins, and reset contracts but advances
# the generated module aliases again. The anchors below were discovered from
# the installed Store ASAR and checked for one exact match before being pinned
# here. Keeping the hash and anchors together prevents a later Store update
# from silently applying the wrong Relay patch.
STORE_26_820_7780_RENDERER_PROFILE = {
    "profile_query_anchor": "async function n2a(){let e=await Ob.safeGet(`/wham/profiles/me`);",
    "profile_query_replacement": (
        "async function n2a(){let e=await(globalThis.CodexMuxWindows?.profileData?.()??"
        "Ob.safeGet(`/wham/profiles/me`));"
    ),
    "usage_status_anchor": (
        "let e=await Ob.safeGet(`/wham/usage`,{additionalHeaders:{\"OAI-App-Brand\":wb}})"
    ),
    "usage_status_replacement": (
        "let e=typeof globalThis.CodexMuxWindows?.usageStatus===`function`?"
        "await globalThis.CodexMuxWindows.usageStatus():{}"
    ),
    "plugin_request_anchor": (
        "async sendRequest(e,t,n){if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);"
        "return e===`config/read`?this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,"
        "e===`plugin/list`&&n?.timeoutMs==null?{...n,timeoutMs:HLt}:n)}"
    ),
    "plugin_request_replacement": (
        "async sendRequest(e,t,n){t=globalThis.CodexMuxWindows?.scopePluginRequest?.(e,t)??t;"
        "if(this.dispatchMessage==null)throw Error(`AppServerRequestClient is missing a message dispatcher`);"
        "return e===`config/read`?this.sendConfigReadRequest(t,n):this.enqueueRequest(e,t,"
        "e===`plugin/list`&&n?.timeoutMs==null?{...n,timeoutMs:HLt}:n)}"
    ),
    "reset_query_anchor": (
        "function WAa(){let e=(0,uH.c)(1),t;return e[0]===Symbol.for(`react.memo_cache_sentinel`)?"
        "(t={queryKey:[`rate-limit-reset-credits`],queryFn:GAa,refetchInterval:nm.ONE_MINUTE,"
        "staleTime:nm.FIVE_SECONDS},e[0]=t):t=e[0],Lt(t)}"
    ),
    "reset_query_replacement": (
        "function WAa(){let n=(0,d1.useSyncExternalStore)("
        "e=>globalThis.CodexMuxWindows?.subscribeReset?.(e)??(()=>{}),"
        "()=>globalThis.CodexMuxWindows?.getResetAccountId?.()??null,()=>null);"
        "return Lt({queryKey:[`rate-limit-reset-credits`,n??`primary`],"
        "queryFn:()=>globalThis.CodexMuxWindows?.rateLimitResets?.(n??`primary`)??GAa(),"
        "refetchInterval:nm.ONE_MINUTE,staleTime:nm.FIVE_SECONDS})}"
    ),
    "reset_mutation_anchor": (
        "function KAa(){let e=(0,uH.c)(3),t=lt(),n=AS(),r;return e[0]!==n||e[1]!==t?"
        "(r={mutationFn:qAa,onSuccess:(e,r)=>{let{creditId:i}=r,a=e.code;"
        "if(a===`reset`||a===`already_redeemed`){let n=e.code===`reset`?e.credit?.id??i:i;"
        "t.setQueryData([`rate-limit-reset-credits`],e=>gAa(e,a,n))}"
        "Promise.all([n([`rate-limit-status`]),n([`rate-limit-reset-credits`])])}},"
        "e[0]=n,e[1]=t,e[2]=r):r=e[2],$t(r)}"
    ),
    "reset_mutation_replacement": (
        "function KAa(){let e=lt(),t=AS(),n=globalThis.CodexMuxWindows?.getResetAccountId?.()??null,"
        "r=[`rate-limit-reset-credits`,n??`primary`];return $t({mutationFn:n?"
        "i=>globalThis.CodexMuxWindows.consumeRateLimitReset(n,i):qAa,onSuccess:(n,i)=>{"
        "let{creditId:a}=i,o=n.code;if(o===`reset`||o===`already_redeemed`){"
        "let s=o===`reset`?n.credit?.id??a:a;e.setQueryData(r,e=>gAa(e,o,s))}"
        "Promise.all([t([`rate-limit-status`]),t(r)])}})}"
    ),
    "selected_usage_anchor": "let y=v;if(g!=null){",
    "selected_usage_replacement": (
        "let y=globalThis.CodexMuxWindows?.selectedResetUsageWindows?.()??v;if(g!=null){"
    ),
    "reset_header_anchor": (
        "let ve;t[46]===ge?ve=t[47]:(ve=(0,d1.jsxs)(bz,{children:[ge,_e]}),"
        "t[46]=ge,t[47]=ve);"
    ),
    "reset_header_replacement": (
        "let ve=(0,d1.jsxs)(bz,{children:[ge,_e,(0,d1.jsx)(`codex-mux-reset-picker`,{})]});"
    ),
    "profile_picker_anchor": "Dn=(0,$.jsxs)(Ct,{ref:be,header:Gt,children:[Sn,wn,Tn]})",
    "profile_picker_replacement": (
        "Dn=(0,$.jsxs)(Ct,{ref:be,header:Gt,children:[(0,$.jsx)(`codex-mux-profile-picker`,{}),Sn,wn,Tn]})"
    ),
    "plugin_picker_anchor": "I=(0,D.jsx)(m,{title:O,subtitle:k,action:F,children:w})",
    "plugin_picker_replacement": (
        "I=(0,D.jsx)(m,{title:O,subtitle:k,action:F,children:(0,D.jsxs)(`div`,"
        "{className:`contents`,children:[(0,D.jsx)(`codex-mux-plugin-picker`,{}),w]})})"
    ),
}

# Reviewed against Store 26.820.9563.0 on 2026-08-28. The profile is kept as
# data because this release changed every minified symbol. Structural discovery
# produced the exact anchors, the full Windows patch suite accepted them, and
# probe_unified_provider.py proved custom Responses request/resume compatibility
# without reading user credentials.
STORE_26_820_9563_RENDERER_PROFILE = json.loads(
    (ROOT / "scripts" / "windows_profiles" / "store-26.820.9563.json").read_text(encoding="utf-8")
)

LEGACY_RENDERER_PROFILE = {
    "profile_query_anchor": PROFILE_QUERY_ANCHOR,
    "profile_query_replacement": PROFILE_QUERY_REPLACEMENT,
    "usage_status_anchor": USAGE_STATUS_ANCHOR,
    "usage_status_replacement": USAGE_STATUS_REPLACEMENT,
    "plugin_request_anchor": PLUGIN_REQUEST_ANCHOR,
    "plugin_request_replacement": PLUGIN_REQUEST_REPLACEMENT,
    "reset_query_anchor": RESET_QUERY_ANCHOR,
    "reset_query_replacement": RESET_QUERY_REPLACEMENT,
    "reset_mutation_anchor": RESET_MUTATION_ANCHOR,
    "reset_mutation_replacement": RESET_MUTATION_REPLACEMENT,
    "selected_usage_anchor": SELECTED_USAGE_ANCHOR,
    "selected_usage_replacement": SELECTED_USAGE_REPLACEMENT,
    "reset_header_anchor": RESET_HEADER_ANCHOR,
    "reset_header_replacement": RESET_HEADER_REPLACEMENT,
    "profile_picker_anchor": PROFILE_PICKER_ANCHOR,
    "profile_picker_replacement": PROFILE_PICKER_REPLACEMENT,
    "plugin_picker_anchor": PLUGIN_PICKER_ANCHOR,
    "plugin_picker_replacement": PLUGIN_PICKER_REPLACEMENT,
}

WINDOWS_RENDERER_PROFILES = {
    "c7ac6d76cf5f30aa5cb92e1e46561933c06e94e3fe2d6582a04dac18c76f3ed1": LEGACY_RENDERER_PROFILE,
    "71c60b36a782e5597f1ca90abf70dba6a9a6aa4e61f3be69e422be43666a7d70": CURRENT_RENDERER_PROFILE,
    "1eb70e2aa26f2408a3e65817f0974e137b1a7ff6e52e43a184154bd4db2074d1": STORE_26_818_3698_RENDERER_PROFILE,
    "10ca5c476ec300f27079184726498a6e8f13ad25b9b443661288eccf4d930ef4": STORE_26_818_4152_RENDERER_PROFILE,
    "c5d839bc9b122b7ef2a2f0f45186b3e5895923de5b6cef5253c936fe670c0479": STORE_26_818_5229_RENDERER_PROFILE,
    "819b966c725fe9d80a9fd54d0949cc447464c983380dd4ee458437e963713bf1": STORE_26_818_5345_RENDERER_PROFILE,
    "e2f04d6aa921d07981b42368df0a28a8bebe8cd21375d4a1f9286757b51c1313": STORE_26_818_8289_RENDERER_PROFILE,
    "5df8bf5a9d30742919390ab11fa419e83aab0891152569a42c6ea4abf15386c2": STORE_26_820_7780_RENDERER_PROFILE,
    "e353c580ef4939d36f4ae32a35c896d089205c1d06b9f711cf78ffa4a3578a8a": STORE_26_820_9563_RENDERER_PROFILE,
}
TESTED_ASAR_HASHES = set(WINDOWS_RENDERER_PROFILES)


def app_server_compatibility_profile(actual_hash: str, renderer_profile: dict[str, str] | None) -> str:
    """Return a handoff capability marker only for an exact reviewed build."""
    if renderer_profile is None or actual_hash not in WINDOWS_RENDERER_PROFILES:
        return "unknown"
    return f"windows-reviewed-{actual_hash}"
# The browser-login bridge is injected into the standard main-renderer preload
# and main-process bundle of the supported ASAR. The installer checks the ASAR
# hash before it reaches these anchors, and both replacements must match once.
LOGIN_PRELOAD_TRAILER_ANCHOR = "\n//# sourceMappingURL=preload.js.map"
LOGIN_MAIN_TRAILER_ANCHOR = "exports.runMainAppStartup=cje;\n//# sourceMappingURL="
LOGIN_MAIN_TRAILER_PATTERN = re.compile(
    r"exports\.runMainAppStartup=([^;]+);[ \t]*(?:\n[ \t]*)?//# sourceMappingURL="
)

# Keep the Store discovery and Router-only upgrade actions in small, explicit
# PowerShell snippets. Both receive paths through a private child-process
# environment variable rather than string interpolation, so a user profile
# path cannot change the command being run.
STORE_PACKAGE_DISCOVERY_SCRIPT = r"""
$ErrorActionPreference = 'Continue'
$packages = @()
try {
    $packages = @(
        Get-AppxPackage -Name 'OpenAI.Codex*' -ErrorAction Stop |
            Where-Object {
                $_.InstallLocation -and
                $_.Architecture -eq 'X64' -and
                (Test-Path -LiteralPath (Join-Path $_.InstallLocation 'app\ChatGPT.exe') -PathType Leaf) -and
                (Test-Path -LiteralPath (Join-Path $_.InstallLocation 'app\resources\app.asar') -PathType Leaf)
            } |
            Sort-Object -Property Version -Descending
    )
} catch {
    # Some Store installations deny the Appx module query to a normal user.
    # The read-only process registration is an equivalent path lookup and does
    # not open or modify the official package.
    $packages = @()
}
if ($packages.Count -gt 0) {
    [Console]::Out.Write($packages[0].InstallLocation)
    exit 0
}
$processes = @(
    Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
        Where-Object {
            $_.ExecutablePath -and
            $_.ExecutablePath -like '*\OpenAI.Codex_*\app\ChatGPT.exe'
        } |
        Sort-Object -Property ProcessId
)
if ($processes.Count -gt 0) {
    [Console]::Out.Write((Split-Path -Parent (Split-Path -Parent $processes[0].ExecutablePath)))
}
""".strip()

STOP_ROUTER_PROCESSES_SCRIPT = r"""
$ErrorActionPreference = 'Stop'
$installRoot = [System.IO.Path]::GetFullPath($env:CODEX_MUX_ROUTER_INSTALL_ROOT).TrimEnd('\')
$prefix = $installRoot + '\'
function Get-RouterProcesses {
    @(Get-CimInstance Win32_Process -ErrorAction Stop | Where-Object {
        $executablePath = $_.ExecutablePath
        if ([string]::IsNullOrWhiteSpace($executablePath)) { return $false }
        try {
            $fullPath = [System.IO.Path]::GetFullPath($executablePath)
            return $fullPath.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)
        } catch {
            return $false
        }
    })
}

$routerProcesses = Get-RouterProcesses
foreach ($process in $routerProcesses) {
    Stop-Process -Id $process.ProcessId -ErrorAction SilentlyContinue
}

$deadline = (Get-Date).AddSeconds(8)
do {
    Start-Sleep -Milliseconds 250
    $routerProcesses = Get-RouterProcesses
} while ($routerProcesses.Count -gt 0 -and (Get-Date) -lt $deadline)

# A copied Electron process can retain a child after its UI has exited. Force
# only the remaining processes whose executable lives below the managed Router
# install root; the Store package has a different path and is never selected.
foreach ($process in $routerProcesses) {
    Stop-Process -Id $process.ProcessId -Force -ErrorAction SilentlyContinue
}
Start-Sleep -Milliseconds 100
if ((Get-RouterProcesses).Count -gt 0) {
    throw "The existing Codex Relay process did not exit. Close that Relay copy and run the installer again."
}
""".strip()

CREATE_SHORTCUT_SCRIPT = r"""
$ErrorActionPreference = 'Stop'
$target = $env:CODEX_MUX_SHORTCUT_TARGET
$workingDirectory = $env:CODEX_MUX_SHORTCUT_WORKING_DIRECTORY
$profile = $env:CODEX_MUX_SHORTCUT_PROFILE
if (!(Test-Path -LiteralPath $target -PathType Leaf)) {
    throw "Router executable was not found: $target"
}
$desktop = [Environment]::GetFolderPath([Environment+SpecialFolder]::DesktopDirectory)
if ([string]::IsNullOrWhiteSpace($desktop)) {
    throw 'Windows did not provide a Desktop directory for the current user.'
}
New-Item -ItemType Directory -Path $desktop -Force | Out-Null
$shortcutPath = Join-Path $desktop 'Codex Relay.lnk'
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($shortcutPath)
$shortcut.TargetPath = $target
$shortcut.Arguments = ('--user-data-dir="{0}"' -f $profile)
$shortcut.WorkingDirectory = $workingDirectory
$shortcut.IconLocation = "$target,0"
$shortcut.Description = 'Independent Codex Relay (does not modify the Store app)'
$shortcut.Save()
[Console]::Out.Write($shortcutPath)
""".strip()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", action="version", version=f"%(prog)s {VERSION}")
    parser.add_argument("--source", type=Path, help="Official ChatGPT app directory to copy")
    parser.add_argument(
        "--destination",
        type=Path,
        default=default_destination(),
        help="Managed Router app destination (defaults to the stable per-user location)",
    )
    parser.add_argument("--force", action="store_true", help="Back up and replace an existing router copy")
    parser.add_argument(
        "--allow-untested-source",
        action="store_true",
        help="Try structural anchor discovery for an unrecorded app.asar hash (still fails closed on ambiguity)",
    )
    parser.add_argument(
        "--launch",
        action="store_true",
        help="Launch the independent Router after a successful install",
    )
    parser.add_argument(
        "--no-desktop-shortcut",
        dest="desktop_shortcut",
        action="store_false",
        help="Do not create or repair the current user's Desktop shortcut",
    )
    parser.set_defaults(desktop_shortcut=True)
    return parser.parse_args()


def local_app_data() -> Path:
    return Path(os.environ.get("LOCALAPPDATA", Path.home() / "AppData/Local"))


def router_install_root() -> Path:
    return local_app_data() / ROUTER_APP_NAME


def legacy_router_install_root() -> Path:
    """Return the 0.2.x managed root that can be migrated after staging."""
    return local_app_data() / LEGACY_ROUTER_APP_NAME


def default_destination() -> Path:
    return router_install_root() / ROUTER_APP_DIRECTORY


def legacy_destination() -> Path:
    return legacy_router_install_root() / ROUTER_APP_DIRECTORY


def app_data() -> Path:
    return Path(os.environ.get("APPDATA", Path.home() / "AppData/Roaming"))


def legacy_router_profile_directory() -> Path:
    return app_data() / LEGACY_ROUTER_APP_NAME


def router_profile_directory() -> Path:
    """Use the new profile unless a 0.2.x profile must be preserved.

    Moving an Electron profile while an updater has just closed it is fragile
    and can lose browser/session state. A renamed Relay install therefore
    keeps using the legacy profile until a user starts with a fresh profile.
    Router account state itself stays in ``~/.codex-mux`` in either case.
    """
    canonical = app_data() / ROUTER_APP_NAME
    legacy = legacy_router_profile_directory()
    if not canonical.exists() and legacy.is_dir():
        return legacy
    return canonical


def updater_directory() -> Path:
    """Return the stable directory that is outside the replaceable Router app."""
    return local_app_data() / f"{ROUTER_APP_NAME} Updater"


def updater_destination() -> Path:
    return updater_directory() / "router-updater.exe"


def is_within(path: Path, parent: Path) -> bool:
    try:
        resolved_path = os.path.normcase(str(path.resolve()))
        resolved_parent = os.path.normcase(str(parent.resolve()))
        return os.path.commonpath((resolved_path, resolved_parent)) == resolved_parent
    except ValueError:
        return False


def validate_managed_destination(destination: Path) -> Path:
    """Accept only the stable per-user destination controlled by this installer."""
    resolved = destination.expanduser().resolve()
    expected = default_destination().resolve()
    if os.path.normcase(str(resolved)) != os.path.normcase(str(expected)):
        raise RuntimeError(
            f"destination must be the managed per-user path: {expected}; got {resolved}"
        )
    if resolved == Path(resolved.anchor) or resolved == Path.home().resolve():
        raise RuntimeError("refusing an unsafe Router destination")
    return resolved


def source_from_store_package() -> Path | None:
    """Find the official Store package without depending on a running app."""
    result = subprocess.run(
        [
            "powershell.exe",
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            STORE_PACKAGE_DISCOVERY_SCRIPT,
        ],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
    )
    install_location = result.stdout.strip()
    if not install_location:
        return None
    candidate = Path(install_location) / "app"
    if (candidate / "ChatGPT.exe").is_file() and (candidate / "resources" / "app.asar").is_file():
        return candidate
    return None


def source_from_running_process() -> Path | None:
    """Fallback for an older Store installation that package discovery cannot read."""
    process_path = subprocess.run(
        [
            "powershell.exe",
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            "(Get-Process -Name ChatGPT -ErrorAction SilentlyContinue | "
            "Where-Object { $_.Path -like '*OpenAI.Codex*' } | "
            "Select-Object -First 1 -ExpandProperty Path)",
        ],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
    ).stdout.strip()
    if process_path:
        candidate = Path(process_path).parent
        if (candidate / "resources" / "app.asar").is_file():
            return candidate
    return None


def source_from_windowsapps_glob() -> Path | None:
    """Last-resort discovery for systems whose WindowsApps ACL permits it."""
    # WindowsApps commonly denies directory enumeration to ordinary users, so
    # this remains a fallback after the package registration lookup above.
    program_files = Path(os.environ.get("ProgramFiles", r"C:\Program Files"))
    windows_apps = program_files / "WindowsApps"
    try:
        candidates = [
            entry / "app"
            for entry in windows_apps.glob(f"{APP_FAMILY_PREFIX}*")
            if "_x64__" in entry.name and (entry / "app" / "ChatGPT.exe").is_file()
        ]
    except OSError:
        return None
    if candidates:
        return max(candidates, key=lambda path: path.stat().st_mtime)
    return None


def default_source() -> Path:
    for discover in (
        source_from_store_package,
        source_from_running_process,
        source_from_windowsapps_glob,
    ):
        candidate = discover()
        if candidate is not None:
            return candidate
    raise RuntimeError(
        "could not locate the Microsoft Store ChatGPT installation; "
        "open the official ChatGPT app or pass --source"
    )


def build(output: Path, package: str) -> None:
    subprocess.run(["go", "build", "-trimpath", "-ldflags=-s -w", "-o", str(output), package], cwd=ROOT, check=True)


def install_updater_helper(binary: Path) -> Path:
    """Install the updater beside, rather than inside, the managed app.

    A helper launched by the previous Router instance can still have its EXE
    open during an update.  In that case retain the known-good helper; it is
    deliberately backwards-compatible and can install the new source release
    even when the new UI has already been staged.
    """
    destination = updater_destination().resolve()
    destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = destination.with_suffix(".new")
    try:
        shutil.copy2(binary, temporary)
        os.replace(temporary, destination)
    except OSError as error:
        temporary.unlink(missing_ok=True)
        if not destination.is_file():
            raise RuntimeError(f"could not install the Router update helper: {error}") from error
        print(f"Keeping the existing Router update helper while it is in use: {error}")
    return destination


def copy_store_app(source: Path, staged: Path) -> None:
    """Copy a Store app while preserving harmless reparse points.

    Recent Store builds contain an optional CUA JavaScript dependency cache
    made of package-link reparse points.  Ordinary users can read the app but
    cannot resolve a few dangling links, which makes a plain ``copytree``
    abort even though Windows CUA is not part of this port.  Keep real files
    and symlink metadata, and omit only that optional cache.
    """
    def ignore_optional_cache(directory: str, names: list[str]) -> set[str]:
        path = Path(directory)
        if "cua_node" in path.parts and path.name == "dist" and "js-dependency-cache" in names:
            return {"js-dependency-cache"}
        return set()

    shutil.copytree(
        source,
        staged,
        symlinks=True,
        ignore_dangling_symlinks=True,
        ignore=ignore_optional_cache,
    )


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def require_asar_tool() -> tuple[str, Path]:
    node = shutil.which("node")
    asar = ROOT / "node_modules" / "@electron" / "asar" / "bin" / "asar.mjs"
    if node is None:
        raise RuntimeError("Node.js is required to patch the renderer")
    if not asar.is_file():
        raise RuntimeError("ASAR build tool is missing; run `npm ci --ignore-scripts` first")
    return node, asar


def run_asar(node: str, asar: Path, *arguments: str) -> subprocess.CompletedProcess[str]:
    command = [node, str(asar), *arguments]
    completed = subprocess.run(
        command,
        cwd=ROOT,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if completed.returncode != 0:
        detail = (completed.stderr or completed.stdout or "unknown ASAR failure").strip()
        raise RuntimeError(f"ASAR {arguments[0] if arguments else 'command'} failed: {detail}")
    return completed


def replace_once(source: str, anchor: str, replacement: str, description: str) -> str:
    count = source.count(anchor)
    if count != 1:
        raise RuntimeError(
            f"could not verify the Windows {description} anchor (expected 1, found {count})"
        )
    return source.replace(anchor, replacement, 1)


def asset_with_anchor(assets: Path, pattern: str, anchor: str, description: str) -> Path:
    matches = [
        path
        for path in assets.glob(pattern)
        if anchor in path.read_text(encoding="utf-8")
    ]
    if len(matches) != 1:
        raise RuntimeError(
            f"could not find exactly one Windows {description} asset "
            f"(found {len(matches)})"
        )
    return matches[0]


# Unknown Store builds are not trusted merely because a few human-readable
# strings still exist. The structural fallback below treats the last reviewed
# renderer profiles as templates: JavaScript identifiers outside quoted strings
# are captured and must remain consistent wherever the minifier reuses them.
# This lets a routine Codex update roll its aliases forward without requiring a
# new hash entry, while a moved/duplicated feature still fails closed.
_DYNAMIC_TEMPLATE_RESERVED = {
    "async", "await", "case", "catch", "const", "default", "else", "Error",
    "false", "finally", "for", "function", "if", "in", "instanceof", "let",
    "new", "null", "of", "return", "switch", "throw", "true", "try", "typeof",
    "var", "void", "while", "this",
}


def _identifier_spans_outside_strings(source: str) -> list[tuple[int, int, str]]:
    spans: list[tuple[int, int, str]] = []
    quote: str | None = None
    index = 0
    while index < len(source):
        character = source[index]
        if quote is not None:
            if character == "\\":
                index += 2
                continue
            if character == quote:
                quote = None
            index += 1
            continue
        if character in "'\"`":
            quote = character
            index += 1
            continue
        if character == "_" or character == "$" or character.isalpha():
            end = index + 1
            while end < len(source) and (source[end] == "_" or source[end] == "$" or source[end].isalnum()):
                end += 1
            spans.append((index, end, source[index:end]))
            index = end
            continue
        index += 1
    return spans


def _dynamic_template_pattern(template: str) -> tuple[str, dict[str, str]]:
    groups: dict[str, str] = {}
    parts: list[str] = []
    cursor = 0
    for start, end, token in _identifier_spans_outside_strings(template):
        parts.append(re.escape(template[cursor:start]))
        if token in _DYNAMIC_TEMPLATE_RESERVED:
            parts.append(re.escape(token))
        elif token in groups:
            parts.append(f"(?P={groups[token]})")
        else:
            name = f"identifier_{len(groups)}"
            groups[token] = name
            parts.append(f"(?P<{name}>[A-Za-z_$][A-Za-z0-9_$]*)")
        cursor = end
    parts.append(re.escape(template[cursor:]))
    return "".join(parts), groups


def _discover_dynamic_anchor(source: str, template: str, description: str) -> tuple[str, dict[str, str], int]:
    pattern, groups = _dynamic_template_pattern(template)
    matches = list(re.finditer(pattern, source))
    if len(matches) != 1:
        raise RuntimeError(
            f"could not safely infer {description} from an untested Windows build "
            f"(expected one structural match, found {len(matches)})"
        )
    match = matches[0]
    values = {token: match.group(group) for token, group in groups.items()}
    return match.group(0), values, match.start()


def _discover_dynamic_anchor_near(
    source: str,
    template: str,
    description: str,
    near_start: int,
    *,
    maximum_distance: int = 50_000,
) -> tuple[str, dict[str, str], int]:
    """Resolve a repeated minified shape only within one reviewed component.

    Some tiny expressions (notably the selected Usage window assignment) are
    duplicated throughout newer bundles. The reset-sheet header is a stronger
    unique anchor in the same component, so structural discovery accepts only
    a single nearest match within a small, explicit byte distance.
    """
    pattern, groups = _dynamic_template_pattern(template)
    matches = list(re.finditer(pattern, source))
    ranked = sorted((abs(match.start() - near_start), match) for match in matches)
    if not ranked or ranked[0][0] > maximum_distance or (len(ranked) > 1 and ranked[0][0] == ranked[1][0]):
        raise RuntimeError(
            f"could not safely infer {description} near its reviewed component "
            f"(found {len(matches)} structural matches)"
        )
    match = ranked[0][1]
    values = {token: match.group(group) for token, group in groups.items()}
    return match.group(0), values, match.start()


def _replace_dynamic_identifiers(source: str, values: dict[str, str]) -> str:
    output: list[str] = []
    cursor = 0
    for start, end, token in _identifier_spans_outside_strings(source):
        output.append(source[cursor:start])
        output.append(values.get(token, token))
        cursor = end
    output.append(source[cursor:])
    return "".join(output)


def _nearby_jsx_alias(source: str, anchor_start: int) -> str:
    prefix = source[max(0, anchor_start - 2_000):anchor_start]
    aliases = re.findall(r"\(0,([A-Za-z_$][A-Za-z0-9_$]*)\.jsx\)", prefix)
    if not aliases:
        raise RuntimeError(
            "could not safely infer the Profile JSX namespace from an untested Windows build"
        )
    return aliases[-1]


def discover_renderer_profile(extracted: Path) -> dict[str, str]:
    """Infer a renderer profile from unique semantic anchors.

    The returned profile contains the *actual* anchors and minifier aliases in
    the extracted build, so the existing exact-once patcher remains the final
    guard. Discovery is intentionally conservative and is only used when the
    caller explicitly opted into an untested source.
    """
    assets = extracted / "webview" / "assets"
    if not assets.is_dir():
        raise RuntimeError("could not find the Windows renderer assets directory")
    candidates = (
        CURRENT_RENDERER_PROFILE,
        LEGACY_RENDERER_PROFILE,
        STORE_26_818_3698_RENDERER_PROFILE,
        STORE_26_818_4152_RENDERER_PROFILE,
    )
    failures: list[str] = []
    for template in candidates:
        try:
            initial_paths = list(assets.glob("app-initial-*.js"))
            if not initial_paths:
                raise RuntimeError("could not find app-initial renderer asset")
            initial_matches: list[Path] = []
            for path in initial_paths:
                source = path.read_text(encoding="utf-8")
                try:
                    _discover_dynamic_anchor(source, template["profile_query_anchor"], "Profile query")
                except RuntimeError as error:
                    if "found 0" not in str(error):
                        raise
                    continue
                initial_matches.append(path)
            if len(initial_matches) != 1:
                raise RuntimeError(
                    f"could not safely infer Profile query asset (found {len(initial_matches)})"
                )
            initial_path = initial_matches[0]
            initial_source = initial_path.read_text(encoding="utf-8")
            dynamic: dict[str, str] = {}
            dynamic_values: dict[str, dict[str, str]] = {}
            dynamic_starts: dict[str, int] = {}
            for key, description in (
                ("profile_query", "Profile query"),
                ("usage_status", "native usage query"),
                ("plugin_request", "Plugins RPC"),
                ("reset_query", "reset query"),
                ("reset_mutation", "reset mutation"),
                ("reset_header", "reset sheet header"),
                ("selected_usage", "usage window"),
            ):
                if key == "selected_usage":
                    anchor, values, start = _discover_dynamic_anchor_near(
                        initial_source,
                        template[f"{key}_anchor"],
                        description,
                        dynamic_starts["reset_header"],
                    )
                else:
                    anchor, values, start = _discover_dynamic_anchor(
                        initial_source, template[f"{key}_anchor"], description
                    )
                dynamic[f"{key}_anchor"] = anchor
                dynamic_values[key] = values
                dynamic_starts[key] = start

            for key, _description in (
                ("profile_query", "Profile query"),
                ("usage_status", "native usage query"),
                ("plugin_request", "Plugins RPC"),
                ("reset_query", "reset query"),
                ("reset_mutation", "reset mutation"),
                ("selected_usage", "usage window"),
                ("reset_header", "reset sheet header"),
            ):
                values = dynamic_values[key]
                replacement = _replace_dynamic_identifiers(
                    template[f"{key}_replacement"], values
                )
                if key == "reset_query" and "useSyncExternalStore" in replacement:
                    template_alias_match = re.search(
                        r"\b([A-Za-z_$][A-Za-z0-9_$]*)\.useSyncExternalStore\b",
                        template[f"{key}_replacement"],
                    )
                    template_alias = template_alias_match.group(1) if template_alias_match else ""
                    mapped_aliases = {
                        mappings[template_alias]
                        for mappings in dynamic_values.values()
                        if template_alias in mappings
                    }
                    if len(mapped_aliases) == 1:
                        react_alias = next(iter(mapped_aliases))
                    else:
                        react_aliases = set(re.findall(
                            r"\b([A-Za-z_$][A-Za-z0-9_$]*)\.useSyncExternalStore\b",
                            initial_source,
                        ))
                        if len(react_aliases) != 1:
                            raise RuntimeError(
                                "could not safely infer the React subscription namespace for reset query"
                            )
                        react_alias = next(iter(react_aliases))
                    if not react_alias:
                        raise RuntimeError(
                            "could not safely infer the React subscription namespace for reset query"
                        )
                    replacement = re.sub(
                        r"\b[A-Za-z_$][A-Za-z0-9_$]*\.useSyncExternalStore\b",
                        f"{react_alias}.useSyncExternalStore",
                        replacement,
                        count=1,
                    )
                dynamic[f"{key}_replacement"] = replacement

            profile_paths = list(assets.glob("profile-*.js"))
            profile_matches: list[tuple[Path, str, dict[str, str], int]] = []
            profile_fallback_matches: list[tuple[Path, str, dict[str, str], int]] = []
            profile_pattern, profile_groups = _dynamic_template_pattern(
                template["profile_picker_anchor"]
            )
            for path in profile_paths:
                source = path.read_text(encoding="utf-8")
                for match in re.finditer(profile_pattern, source):
                    values = {
                        token: match.group(group)
                        for token, group in profile_groups.items()
                    }
                    profile_fallback_matches.append(
                        (path, match.group(0), values, match.start())
                    )
                    # The actual Profile settings body is the one repeated
                    # shape owned by a header-bearing settings container. New
                    # builds reuse the short children array in unrelated cards.
                    context = source[max(0, match.start() - 250):match.start()]
                    if "header:" not in context:
                        continue
                    identifier = r"[A-Za-z_$][A-Za-z0-9_$]*"
                    container_pattern = re.compile(
                        rf"{identifier}=\(0,{identifier}\.jsxs\)\({identifier},"
                        rf"\{{ref:{identifier},header:{identifier},{re.escape(match.group(0))}\}}\)"
                    )
                    window_start = max(0, match.start() - 250)
                    window = source[window_start:match.end() + 8]
                    containers = list(container_pattern.finditer(window))
                    if len(containers) != 1:
                        continue
                    container = containers[0]
                    full_anchor = container.group(0)
                    full_start = window_start + container.start()
                    profile_matches.append((path, full_anchor, values, full_start))
            if not profile_matches and len(profile_fallback_matches) == 1:
                profile_matches = profile_fallback_matches
            if len(profile_matches) != 1:
                raise RuntimeError(
                    f"could not safely infer Profile settings asset (found {len(profile_matches)})"
                )
            profile_path, profile_anchor, profile_values, profile_start = profile_matches[0]
            profile_replacement = _replace_dynamic_identifiers(
                template["profile_picker_replacement"], profile_values
            )
            if "(0,$.jsx)" in profile_replacement:
                profile_replacement = profile_replacement.replace(
                    "(0,$.jsx)", f"(0,{_nearby_jsx_alias(profile_path.read_text(encoding='utf-8'), profile_start)}.jsx)"
                )
            short_profile_anchor = _replace_dynamic_identifiers(
                template["profile_picker_anchor"], profile_values
            )
            if profile_anchor != short_profile_anchor:
                if profile_anchor.count(short_profile_anchor) != 1:
                    raise RuntimeError("could not safely expand the Profile settings anchor")
                profile_replacement = profile_anchor.replace(
                    short_profile_anchor, profile_replacement, 1
                )
            dynamic["profile_picker_anchor"] = profile_anchor
            dynamic["profile_picker_replacement"] = profile_replacement

            plugin_paths = [
                path for path in assets.glob("plugins-settings-*.js")
                if not path.name.startswith("plugins-settings-row-")
            ]
            plugin_matches: list[tuple[Path, str, dict[str, str], int]] = []
            for path in plugin_paths:
                source = path.read_text(encoding="utf-8")
                try:
                    plugin_matches.append((path, *_discover_dynamic_anchor(
                        source, template["plugin_picker_anchor"], "Plugins settings"
                    )))
                except RuntimeError as error:
                    if "found 0" not in str(error):
                        raise
                    continue
            if len(plugin_matches) != 1:
                raise RuntimeError(
                    f"could not safely infer Plugins settings asset (found {len(plugin_matches)})"
                )
            _plugin_path, plugin_anchor, plugin_values, _plugin_start = plugin_matches[0]
            dynamic["plugin_picker_anchor"] = plugin_anchor
            dynamic["plugin_picker_replacement"] = _replace_dynamic_identifiers(
                template["plugin_picker_replacement"], plugin_values
            )
            return dynamic
        except RuntimeError as error:
            failures.append(str(error))
    raise RuntimeError(
        "the untested Windows renderer did not pass structural compatibility checks; "
        "review the new Codex build before installing it (" + failures[-1] + ")"
    )


def patch_windows_feature_bundles(
    extracted: Path,
    renderer_profile: dict[str, str] | None = None,
) -> None:
    """Patch only the reviewed renderer slots for one ASAR profile."""
    profile = renderer_profile or LEGACY_RENDERER_PROFILE
    assets = extracted / "webview" / "assets"
    if not assets.is_dir():
        raise RuntimeError("could not find the Windows renderer assets directory")

    initial_path = asset_with_anchor(
        assets, "app-initial-*.js", profile["profile_query_anchor"], "profile query"
    )
    initial = initial_path.read_text(encoding="utf-8")
    initial = replace_once(
        initial,
        profile["profile_query_anchor"],
        profile["profile_query_replacement"],
        "profile query",
    )
    initial = replace_once(
        initial,
        profile["usage_status_anchor"],
        profile["usage_status_replacement"],
        "native usage query",
    )
    initial = replace_once(
        initial,
        profile["plugin_request_anchor"],
        profile["plugin_request_replacement"],
        "Plugins RPC",
    )
    initial = replace_once(
        initial,
        profile["reset_query_anchor"],
        profile["reset_query_replacement"],
        "reset query",
    )
    initial = replace_once(
        initial,
        profile["reset_mutation_anchor"],
        profile["reset_mutation_replacement"],
        "reset mutation",
    )
    initial = replace_once(
        initial,
        profile["selected_usage_anchor"],
        profile["selected_usage_replacement"],
        "usage window",
    )
    initial = replace_once(
        initial,
        profile["reset_header_anchor"],
        profile["reset_header_replacement"],
        "reset sheet header",
    )
    initial_path.write_text(initial, encoding="utf-8")

    profile_path = asset_with_anchor(
        assets, "profile-*.js", profile["profile_picker_anchor"], "Profile settings"
    )
    profile_text = profile_path.read_text(encoding="utf-8")
    profile_text = replace_once(
        profile_text,
        profile["profile_picker_anchor"],
        profile["profile_picker_replacement"],
        "Profile picker",
    )
    profile_path.write_text(profile_text, encoding="utf-8")

    plugins_path = asset_with_anchor(
        assets,
        "plugins-settings-*.js",
        profile["plugin_picker_anchor"],
        "Plugins settings",
    )
    plugins = plugins_path.read_text(encoding="utf-8")
    plugins = replace_once(
        plugins,
        profile["plugin_picker_anchor"],
        profile["plugin_picker_replacement"],
        "Plugins picker",
    )
    plugins_path.write_text(plugins, encoding="utf-8")


def load_windows_login_asset(filename: str) -> str:
    """Return a reviewed source asset used by the version-pinned ASAR patch."""
    path = ROOT / "ui" / filename
    if not path.is_file():
        raise RuntimeError(f"could not find Windows browser-login asset: {path}")
    source = path.read_text(encoding="utf-8").strip()
    if not source:
        raise RuntimeError(f"Windows browser-login asset is empty: {path}")
    return source


def patch_windows_login_bundles(
    extracted: Path,
    router_version: str = VERSION,
) -> tuple[Path, Path]:
    """Inject the official-browser sign-in bridge into the supported Electron bundle.

    The renderer can call only a small preload API. The main-process companion
    validates the official initial URL and opens it through the user's default
    browser; the isolated app-server owns the localhost callback and token
    exchange, so the remote page never receives Node or Router access.
    """
    build = extracted / ".vite" / "build"
    if not build.is_dir():
        raise RuntimeError("could not find the Windows Electron build directory")

    preload_path = build / "preload.js"
    if not preload_path.is_file():
        raise RuntimeError("could not find the Windows Electron preload bundle")
    preload = preload_path.read_text(encoding="utf-8")
    preload_patch = load_windows_login_asset("windows-router-login-preload.js")
    update_preload_patch = load_windows_login_asset("windows-router-update-preload.js")
    preload = replace_once(
        preload,
        LOGIN_PRELOAD_TRAILER_ANCHOR,
        f"\n{preload_patch}\n{update_preload_patch}{LOGIN_PRELOAD_TRAILER_ANCHOR}",
        "browser login preload",
    )
    preload_path.write_text(preload, encoding="utf-8")

    main_candidates = []
    for path in build.glob("main-*.js"):
        source = path.read_text(encoding="utf-8")
        if len(LOGIN_MAIN_TRAILER_PATTERN.findall(source)) == 1:
            main_candidates.append((path, source))
    if len(main_candidates) != 1:
        raise RuntimeError(
            "could not find exactly one Windows browser login main-process "
            f"asset (found {len(main_candidates)})"
        )
    main_path, main = main_candidates[0]
    main = main_path.read_text(encoding="utf-8")
    main_patch = load_windows_login_asset("windows-router-login-main.js")
    update_main_patch = load_windows_login_asset("windows-router-update-main.js")
    for placeholder, value in (
        ("__CODEX_MUX_ROUTER_VERSION__", router_version),
        (
            "__CODEX_MUX_UPDATE_MANIFEST_URL__",
            "https://github.com/LightHaru/codex-relay/releases/latest/download/windows-update.json",
        ),
    ):
        update_main_patch = update_main_patch.replace(placeholder, value)
    insertion = f"exports.runMainAppStartup={{STARTUP}};\n{main_patch}\n{update_main_patch}\n//# sourceMappingURL="
    match = LOGIN_MAIN_TRAILER_PATTERN.search(main)
    if match is None:
        raise RuntimeError("could not verify the Windows browser login main-process anchor")
    startup = match.group(1)
    # The injected JavaScript contains many object-literal braces, so use a
    # literal placeholder replacement rather than str.format().
    replacement = insertion.replace("{STARTUP}", startup, 1)
    main = main[: match.start()] + replacement + main[match.end() :]
    main_path.write_text(main, encoding="utf-8")
    return preload_path, main_path


def patch_windows_product_metadata(extracted: Path) -> None:
    """Brand only the copied Electron bundle as Codex Relay.

    ``productName`` is what Electron uses for the window/taskbar display
    name.  The package ``name`` and ``codexAppBrand`` remain upstream values;
    changing those would alter protocol feature flags and server routing.
    """
    package_path = extracted / "package.json"
    if not package_path.is_file():
        raise RuntimeError("could not find the Windows Electron package metadata")
    try:
        package = json.loads(package_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise RuntimeError(f"could not read the Windows Electron package metadata: {error}") from error
    if not isinstance(package, dict):
        raise RuntimeError("Windows Electron package metadata is not an object")
    if not isinstance(package.get("name"), str) or not package["name"].strip():
        raise RuntimeError("Windows Electron package metadata has no internal package name")
    package["productName"] = ROUTER_APP_NAME
    package["description"] = "Codex Relay"
    # The Store build also carries the package identity used by its native
    # Windows taskbar/AppUserModel registration. Keep the upstream app brand
    # and internal package name intact, but give the copied process a stable
    # Relay-only identity so Windows never groups it with ChatGPT.
    if "codexWindowsPackageIdentity" in package:
        package["codexWindowsPackageIdentity"] = "LightHaru.CodexRelay"
    try:
        package_path.write_text(json.dumps(package, indent=2) + "\n", encoding="utf-8")
    except OSError as error:
        raise RuntimeError(f"could not write the Windows Electron package metadata: {error}") from error


def load_or_create_control_token(state_root: Path) -> str:
    state_root.mkdir(mode=0o700, parents=True, exist_ok=True)
    token_path = state_root / "control-token"
    if token_path.is_file():
        token = token_path.read_text(encoding="utf-8").strip()
        try:
            decoded = bytes.fromhex(token)
        except ValueError as error:
            raise RuntimeError(f"invalid existing control token: {error}") from error
        if len(decoded) != 32:
            raise RuntimeError("invalid existing control token: expected 32 random bytes")
        return token
    token = secrets.token_hex(32)
    temporary = token_path.with_suffix(".tmp")
    temporary.write_text(token, encoding="utf-8")
    os.replace(temporary, token_path)
    return token


def patch_windows_renderer(
    resources: Path,
    temporary: Path,
    token: str,
    *,
    renderer_profile: dict[str, str] | None = None,
    allow_structural_discovery: bool = False,
    router_version: str = VERSION,
) -> None:
    node, asar = require_asar_tool()
    original_asar = resources / "app.asar"
    extracted = temporary / "asar"
    repacked = temporary / "app.asar"
    print("Patching the Windows subscription surfaces…")
    run_asar(node, asar, "extract", str(original_asar), str(extracted))

    # The Store bundle keeps the upstream Electron product name ("Codex"),
    # which makes an installed Relay window indistinguishable from the
    # official app when both are open.  Change only the copied bundle's
    # display metadata; keep the internal package name and API brand intact so
    # the upstream app-server contract is not altered.  The Store tree itself
    # is never edited.
    patch_windows_product_metadata(extracted)

    index_path = extracted / "webview" / "index.html"
    if not index_path.is_file():
        raise RuntimeError("could not find the Windows renderer index.html")
    index = index_path.read_text(encoding="utf-8")
    connect_anchor = "connect-src &#39;self&#39;"
    if index.count(connect_anchor) != 1:
        raise RuntimeError("could not verify the renderer Content Security Policy")
    index = index.replace(
        connect_anchor,
        f"{connect_anchor} http://127.0.0.1:{CONTROL_PORT}",
        1,
    )
    script_name = "codex-mux-windows-menu.js"
    script_tag = f'<script src="./assets/{script_name}"></script>'
    if script_tag in index:
        raise RuntimeError("source renderer already contains the Windows router menu")
    if index.count("</head>") != 1:
        raise RuntimeError("could not find the renderer document head")
    index = index.replace("</head>", f"    {script_tag}\n</head>", 1)
    index_path.write_text(index, encoding="utf-8")

    if renderer_profile is None and allow_structural_discovery:
        print("No reviewed hash profile; checking unique renderer anchors…")
        renderer_profile = discover_renderer_profile(extracted)
        print("Structural renderer compatibility checks passed.")
    if renderer_profile is None:
        raise RuntimeError("Windows renderer profile is required before patching")
    patch_windows_feature_bundles(extracted, renderer_profile)
    login_preload, login_main = patch_windows_login_bundles(extracted, router_version)

    bridge = (ROOT / "ui" / "windows-router-menu.js").read_text(encoding="utf-8")
    if bridge.count("__CODEX_MUX_CONTROL_PORT__") != 1 or bridge.count("__CODEX_MUX_CONTROL_TOKEN__") != 1:
        raise RuntimeError("Windows account-menu bridge placeholders are invalid")
    bridge = bridge.replace("__CODEX_MUX_CONTROL_PORT__", str(CONTROL_PORT), 1)
    bridge = bridge.replace("__CODEX_MUX_CONTROL_TOKEN__", token, 1)
    target_script = extracted / "webview" / "assets" / script_name
    if target_script.exists():
        raise RuntimeError("renderer already contains a Windows router menu asset")
    target_script.write_text(bridge, encoding="utf-8")
    subprocess.run([node, "--check", str(target_script)], cwd=ROOT, check=True)
    subprocess.run([node, "--check", str(login_preload)], cwd=ROOT, check=True)
    subprocess.run([node, "--check", str(login_main)], cwd=ROOT, check=True)

    pack_arguments = [
        "pack",
        "--unpack-dir",
        ASAR_UNPACK_DIRECTORIES,
        "--unpack",
        ASAR_UNPACK_FILES,
        str(extracted),
        str(repacked),
    ]
    run_asar(node, asar, *pack_arguments)
    listing = run_asar(node, asar, "list", "--is-pack", str(repacked)).stdout
    if script_name not in listing:
        raise RuntimeError("repacked ASAR does not contain the Windows router menu")
    unpacked = temporary / "app.asar.unpacked"
    if not unpacked.is_dir():
        raise RuntimeError("ASAR pack did not create the required native unpacked tree")
    shutil.copy2(repacked, original_asar)
    destination_unpacked = resources / "app.asar.unpacked"
    # Keep upstream unpacked modules that the ASAR tool did not emit. Windows
    # may also keep handles in this copied tree briefly after copytree(), so an
    # in-place merge avoids a needless recursive delete and remains atomic for
    # the modified files.
    shutil.copytree(unpacked, destination_unpacked, dirs_exist_ok=True)


def write_launcher(destination: Path) -> Path:
    parent = destination.parent
    profile = router_profile_directory()
    launcher = parent / f"{ROUTER_APP_NAME}.cmd"
    content = (
        "@echo off\r\n"
        f"start \"{ROUTER_APP_NAME}\" /D \"{destination}\" "
        f"\"{destination / 'ChatGPT.exe'}\" --user-data-dir=\"{profile}\" %*\r\n"
    )
    launcher.write_text(content, encoding="utf-8", newline="")
    return launcher


def stop_router_processes(destination: Path) -> None:
    """Stop only executables loaded from the managed Router install root.

    This deliberately uses each process's executable path rather than its
    image name. The independent Router and the Microsoft Store app both use
    ChatGPT.exe; matching on the name would risk closing the Store app.
    """
    install_root = destination.parent
    if not install_root.exists():
        return
    environment = os.environ.copy()
    environment["CODEX_MUX_ROUTER_INSTALL_ROOT"] = str(install_root)
    result = subprocess.run(
        [
            "powershell.exe",
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            STOP_ROUTER_PROCESSES_SCRIPT,
        ],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=environment,
    )
    if result.returncode != 0:
        detail = (result.stderr or result.stdout).strip()
        raise RuntimeError(
            "could not stop the existing Router copy"
            + (f": {detail}" if detail else "")
        )


def create_desktop_shortcut(destination: Path) -> Path:
    """Create or repair a direct per-user Desktop shortcut to the Router copy."""
    target = destination / "ChatGPT.exe"
    if not target.is_file():
        raise RuntimeError(f"cannot create shortcut; Router executable is missing: {target}")
    environment = os.environ.copy()
    environment["CODEX_MUX_SHORTCUT_TARGET"] = str(target)
    environment["CODEX_MUX_SHORTCUT_WORKING_DIRECTORY"] = str(destination)
    environment["CODEX_MUX_SHORTCUT_PROFILE"] = str(router_profile_directory())
    result = subprocess.run(
        [
            "powershell.exe",
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            CREATE_SHORTCUT_SCRIPT,
        ],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=environment,
    )
    if result.returncode != 0:
        detail = (result.stderr or result.stdout).strip()
        raise RuntimeError(
            "could not create the Desktop shortcut"
            + (f": {detail}" if detail else "")
        )
    shortcut = result.stdout.strip()
    return Path(shortcut) if shortcut else Path.home() / "Desktop" / f"{ROUTER_APP_NAME}.lnk"


def launch_router(destination: Path) -> None:
    """Launch the independent copy directly with its dedicated Electron profile."""
    executable = destination / "ChatGPT.exe"
    if not executable.is_file():
        raise RuntimeError(f"cannot launch Router; executable is missing: {executable}")
    subprocess.Popen(
        [str(executable), f"--user-data-dir={router_profile_directory()}"],
        cwd=destination,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )


def next_backup_path(state_root: Path) -> Path:
    """Return a unique, Router-state-owned path for an app replacement backup."""
    stamp = time.strftime("%Y%m%d-%H%M%S")
    backups = state_root / "backups"
    for attempt in range(1_000):
        suffix = "" if attempt == 0 else f"-{attempt}"
        candidate = backups / f"{stamp}{suffix}" / ROUTER_APP_DIRECTORY
        if not candidate.exists() and not candidate.parent.exists():
            return candidate
    raise RuntimeError("could not allocate a unique Router backup directory")


def validate_install_paths(source: Path, destination: Path) -> tuple[Path, Path]:
    source = source.expanduser().resolve()
    destination = validate_managed_destination(destination)
    if source == destination or is_within(source, destination) or is_within(destination, source):
        raise RuntimeError(
            "source and destination must not overlap; the official app is never patched in place"
        )
    return source, destination


def patch(
    source: Path,
    destination: Path,
    force: bool,
    allow_untested: bool,
    *,
    desktop_shortcut: bool = True,
    launch: bool = False,
) -> None:
    source, destination = validate_install_paths(source, destination)
    resources = source / "resources"
    asar = resources / "app.asar"
    if not source.is_dir() or not (source / "ChatGPT.exe").is_file() or not (resources / "codex.exe").is_file() or not asar.is_file():
        raise RuntimeError(f"not a supported Windows ChatGPT app directory: {source}")
    actual_hash = sha256(asar)
    print(f"Source app: {source}\napp.asar SHA-256: {actual_hash}")
    if actual_hash not in TESTED_ASAR_HASHES and not allow_untested:
        raise RuntimeError("source app.asar is not approved; review the update or pass --allow-untested-source")
    renderer_profile = WINDOWS_RENDERER_PROFILES.get(actual_hash)
    if renderer_profile is None:
        print(
            "Source hash is not in the reviewed list; the installer will only "
            "continue if structural compatibility discovery passes."
        )
    if shutil.which("go") is None:
        raise RuntimeError("Go is required to build the mux")
    require_asar_tool()
    legacy_root = legacy_router_install_root().resolve()
    legacy_exists = legacy_root.is_dir() and legacy_root != destination.parent
    if (destination.exists() or legacy_exists) and not force:
        existing = destination if destination.exists() else legacy_root
        raise RuntimeError(
            f"an existing Relay installation was found at {existing} "
            "(pass --force to create a recoverable backup)"
        )
    destination.parent.mkdir(parents=True, exist_ok=True)
    state_root = Path.home() / ".codex-mux"
    token = load_or_create_control_token(state_root)
    # ASAR extraction preserves npm's deeply nested dependency layout. Use the
    # shorter system temp root on Windows to avoid legacy path-length cleanup
    # failures after a successful install. Some Store trees contain symlink-like
    # dependency layouts that Python cannot always remove immediately, so those
    # leftovers are deliberately ignored rather than misreporting a completed
    # replacement as failed.
    with tempfile.TemporaryDirectory(
        prefix="csr-",
        dir=tempfile.gettempdir(),
        ignore_cleanup_errors=True,
    ) as temp:
        staged = Path(temp) / "app"
        proxy = Path(temp) / "codex.exe"
        routerctl = Path(temp) / "routerctl.exe"
        updater = Path(temp) / "router-updater.exe"
        print("Building Windows mux and control CLI…")
        build(proxy, "./cmd/codex-mux")
        build(routerctl, "./cmd/routerctl")
        build(updater, "./cmd/router-updater")
        print("Copying the official app to an independent location…")
        copy_store_app(source, staged)
        staged_resources = staged / "resources"
        patch_windows_renderer(
            staged_resources,
            Path(temp),
            token,
            renderer_profile=renderer_profile,
            allow_structural_discovery=renderer_profile is None and allow_untested,
            router_version=VERSION,
        )
        real_codex = staged_resources / "codex.real.exe"
        if real_codex.exists():
            raise RuntimeError("source already contains codex.real.exe")
        (staged_resources / "codex.exe").rename(real_codex)
        shutil.copy2(proxy, staged_resources / "codex.exe")
        shutil.copy2(routerctl, staged / "routerctl.exe")
        manifest = {
            "version": VERSION,
            "platform": "windows",
            "source": str(source),
            "sourceAsarSha256": actual_hash,
            "appServerCompatibilityProfile": app_server_compatibility_profile(actual_hash, renderer_profile),
            "rendererUi": "windows-renderer-patches-v3-browser-login",
            "profile": str(router_profile_directory()),
        }
        (staged / "codex-relay.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
        # The helper is outside the replaceable app tree, so an update can
        # remain alive while the Router UI exits. Install it before stopping
        # any Router process; a currently running older helper may simply be
        # retained by install_updater_helper().
        helper = install_updater_helper(updater)
        backup = None
        legacy_backup = None
        # Build and patch the new copy before stopping anything. If an
        # upstream update or a renderer-anchor check fails, all current Router
        # copies stay open and untouched. Once staging is ready, stop only
        # Router executables in the managed install root. This also prevents a
        # pre-stable app-ui-v2/app-ui-v3 copy from retaining the control port.
        stop_router_processes(destination)
        if legacy_exists:
            # Stop only the previous managed product root. The Store app has
            # a distinct WindowsApps path and is never in either allow-list.
            stop_router_processes(legacy_destination())
        if destination.exists():
            backup = next_backup_path(state_root)
            backup.parent.mkdir(parents=True, exist_ok=False)
            destination.rename(backup)
            print(f"Existing copy moved to {backup}")
        if legacy_exists and legacy_root.exists():
            legacy_backup = next_backup_path(state_root).parent / LEGACY_ROUTER_APP_NAME
            backups_root = (state_root / "backups").resolve()
            if not is_within(legacy_backup, backups_root):
                raise RuntimeError("refusing an unsafe legacy Router migration target")
            legacy_backup.parent.mkdir(parents=True, exist_ok=False)
            legacy_root.rename(legacy_backup)
            print(f"Legacy Codex Subscription Router copy moved to {legacy_backup}")
        try:
            staged.rename(destination)
        except OSError:
            if backup is not None and backup.exists():
                backup.rename(destination)
            if legacy_backup is not None and legacy_backup.exists():
                legacy_backup.rename(legacy_root)
            raise
    launcher = write_launcher(destination)
    shortcut = create_desktop_shortcut(destination) if desktop_shortcut else None
    if launch:
        launch_router(destination)
    print(
        f"Installed app: {destination}\n"
        f"Launcher: {launcher}\n"
        f"Desktop shortcut: {shortcut or 'not requested'}\n"
        f"Control CLI: {destination / 'routerctl.exe'}\n"
        f"Update helper: {helper}"
    )


def main() -> int:
    args = parse_args()
    try:
        patch(
            args.source or default_source(),
            args.destination,
            args.force,
            args.allow_untested_source,
            desktop_shortcut=args.desktop_shortcut,
            launch=args.launch,
        )
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"Windows patch failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
