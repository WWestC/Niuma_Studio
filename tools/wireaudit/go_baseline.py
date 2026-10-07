#!/usr/bin/env python3
# go_baseline.py — t_139（r_36/p_55）：Go 侧基准面生成器。
# 从真源（wire/wire.go 帧词汇 + server/ws.go 路由注册 + 五 struct 的
# json tag）机器生成 tools/wireaudit/wire-go-baseline.json——三方共用
# schema（评审定稿）：ws/http/structs 三节、行号锚、consumer_hint
# （施工过标注 unknown 不装全知）、unit（Unix 秒/replay ms 例外等
# 雷区机器可读化）。脚本即交付物：未来契约漂移的常驻哨兵候选
# （与 t_202 契约断言互补——那个钉钉子这个扫全量）。
import re, json

out = {'meta': {'generated_by': 'tools/wireaudit/go_baseline.py'}}

# ── ① WS 帧词汇表（真源 wire/wire.go，chat/wire.go 是兼容再导出）──
src = open('wire/wire.go').read()
ws = []
for m in re.finditer(r'(Msg[A-Za-z]+)\s*=\s*"([^"]+)"\s*(?://\s*(.*))?', src):
    name, value, comment = m.group(1), m.group(2), (m.group(3) or '')
    line = src[:m.start()].count('\n') + 1
    direction = 'C->S' if 'C->S' in comment else ('S->C' if 'S->C' in comment else 'both/unknown')
    ws.append({'const': name, 'value': value, 'go': f'wire/wire.go:{line}',
               'direction': direction, 'comment': comment.strip()[:120]})
out['ws'] = ws

# ── ② HTTP 路由面（server 包的两种注册形全扫）──
#  s.route(mux, ...)     —— roles 矩阵门控注册（主通道，106 处）
#  mux.HandleFunc(...)   —— 直注册（矩阵外的服务面：/ws /auth /art 等）
#  mux.Handle(gateRoute) —— Faces 外挂（/dispatch/）
routes = []
import glob
for fname in sorted(glob.glob('server/*.go')):
    if fname.endswith('_test.go'):
        continue
    src_f = open(fname).read()
    for m in re.finditer(r's\.route\(mux,\s*"([^"]+)",\s*(s\.[\w.()]+)\)', src_f):
        line = src_f[:m.start()].count('\n') + 1
        routes.append({'route': m.group(1), 'handler': m.group(2),
                       'go': f'{fname}:{line}', 'gated': True})
    for m in re.finditer(r'mux\.HandleFunc\("([^"]+)",\s*([\w.()]+)\)', src_f):
        line = src_f[:m.start()].count('\n') + 1
        routes.append({'route': m.group(1), 'handler': m.group(2),
                       'go': f'{fname}:{line}', 'gated': False})
    for m in re.finditer(r'mux\.Handle\("([^"]+)",\s*s\.gateRoute', src_f):
        line = src_f[:m.start()].count('\n') + 1
        routes.append({'route': m.group(1), 'handler': '(faces)',
                       'go': f'{fname}:{line}', 'gated': True})
out['http'] = routes

# ── ③ 数据契约：P0 五 struct（Task/Req/Member/Person/Notice）＋
#     P1 次频面——json tag 全提取交双向脚本初筛 ──
STRUCT_FILES = {
    'tasks.Task': ('tasks/tasks.go', r'type Task struct'),
    'requirements.Req': ('requirements/requirements.go', r'type Req struct'),
    'wire.Member': ('wire/wire.go', 'type Member struct'),
    'wire.Notice': ('wire/wire.go', 'type Notice struct'),
    'kb.PersonSummary': ('kb/kb.go', r'type PersonSummary struct'),
    'chat.Notice': ('chat/wire.go', 'type Notice struct'),
    'staffing.Knobs': ('staffing/settings.go', r'type AutoPilotKnobs struct'),
}
structs = {}
for key, (fname, pat) in STRUCT_FILES.items():
    try:
        ssrc = open(fname).read()
    except FileNotFoundError:
        continue
    m = re.search(pat + r'(.*?)\n\}', ssrc, re.S)
    if not m:
        continue
    body = m.group(1)
    start_line = ssrc[:m.start()].count('\n') + 1
    fields = []
    for fm in re.finditer(r'(\w+)\s+\S+\s+`json:"([^",]+)([^"]*)"`', body):
        fname_, tag, rest = fm.group(1), fm.group(2), fm.group(3)
        fields.append({'go_field': fname_, 'json': tag,
                       'omitempty': 'omitempty' in rest})
    structs[key] = {'go': f'{fname}:{start_line}', 'fields': fields}
out['structs'] = structs
out['meta'].update({'ws_count': len(ws), 'http_count': len(routes),
                    'struct_count': len(structs)})

# ── unit 标注（r_36 评审雷区预报的机器可读化）──
# 时间戳：全仓 Unix 秒；replay day 档的 t 是墙钟 ms（唯一例外）。
# 双字段：progress (string 人类注记) vs progress_pct (int 0-100)。
# 词汇表：task 五态 / req 四态（含 parking）——不同枚举。
UNIT_MAP = {
    'ts': 'unix_s', 'created_ts': 'unix_s', 'updated_ts': 'unix_s',
    'closed_ts': 'unix_s', 'joined': 'unix_s', 'start_ts': 'unix_s',
    'end_ts': 'unix_s', 'last_report_ts': 'unix_s', 'park_ts': 'unix_s',
    'review_after': 'unix_s', 'working_since': 'unix_s',
    'progress': 'string（人类注记，如 96% · 差验收）',
    'progress_pct': 'int 0-100',
    'status': 'vocab: task=todo|doing|pending|done|cancelled / req=open|parking|split|closed（不同枚举！）',
    't': 'unix_s（replay day 档例外：墙钟 ms）',
}
for key, st in out['structs'].items():
    for f in st['fields']:
        if f['json'] in UNIT_MAP:
            f['unit'] = UNIT_MAP[f['json']]

# 消费方标注（施工过的面标 known，其余 unknown——不装全知）
KNOWN_CONSUMERS = {
    '/ws': 'both', '/members': 'web', '/zcode': 'web', '/kb': 'web',
    '/kb/manual': 'web', '/kb/people': 'web', '/kb/people/{name}': 'web',
    '/kb/people/{name}/history': 'web', '/kb/tasks': 'both',
    '/kb/tasks/{id}': 'both', '/kb/docs': 'web', '/kb/docs/': 'web',
    '/kb/capacity': 'web', '/kb/establishment': 'web',
    '/kb/establishment/row': 'both', '/visitor': 'web',
    '/achieve/tests-green': 'web', '/view/{project}': 'web(访客)',
    '/dayreplay/{project}': 'web(分享页)', '/p/{key}/tasks': 'both',
    '/p/{key}/reqs': 'both', '/p/{key}/reqs/{id}': 'cli',
    '/p/{key}/reqs/{id}/park': 'cli', '/p/{key}/reqs/{id}/unpark': 'cli',
    '/p/{key}/history': 'both', '/p/{key}/autopilot': 'both',
    '/dispatch': 'both', '/dispatch/birth': 'cli', '/dispatch/model': 'cli',
    '/usage': 'web', '/usage/turns': 'web', '/auth/login': 'web',
    '/auth/logout': 'web', '/auth/me': 'web', '/p/{key}/git': 'web',
    '/p/{key}/git/log': 'web', '/p/{key}/git/graph': 'web',
    '/p/{key}/git/branches': 'web', '/p/{key}/git/seats': 'web',
    '/p/{key}/git/req-activity': 'web', '/p/{key}/schedule': 'web',
    '/p/{key}/meeting': 'web', '/p/{key}/notice': 'web',
    '/p/{key}/staffing': 'web', '/p/{key}/plan': 'web',
    '/p/{key}/questions': 'web', '/p/{key}/reacts': 'web',
    '/p/{key}/reads': 'web', '/p/{key}/acks': 'web',
    '/p/{key}/lifecycle': 'cli', '/p/{key}': 'cli(patch)',
    '/p/{key}/replay': 'web(房主页)', '/p/{key}/term': 'web',
    '/p/{key}/term/file': 'web', '/p/{key}/trace': 'web',
    '/p/{key}/trace/file': 'web',
}
for r in out['http']:
    r['consumer_hint'] = KNOWN_CONSUMERS.get(r['route'], 'unknown')

json.dump(out, open('tools/wireaudit/wire-go-baseline.json', 'w'), ensure_ascii=False, indent=1)
print(f"ws {len(ws)} | http {len(routes)} | structs {len(structs)} → tools/wireaudit/wire-go-baseline.json")
