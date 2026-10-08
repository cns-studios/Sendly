#!/usr/bin/env python3
"""Applies the legal Markdown files to Sendly's legal page templates.

The Markdown files in legal/ are the source of truth. For every page below this
script overwrites everything between <main> and </main> in the Go template with
the rendered Markdown; the rest of the template (head, header, footer) is left
alone. Running it twice gives the same result.

Usage: python3 scripts/build_legal.py [templates_dir] [legal_dir]   (from the repo root)

Supported Markdown: # title, ## / ### headings, paragraphs (line breaks kept),
- and 1. lists, pipe tables, **bold**, *italic*, [links](url), bare URLs and
e-mail addresses, backslash escapes and [PLACEHOLDER] markers.
"""
import html
import re
import sys
from pathlib import Path

PAGES = [
    {'src': 'TERMS_OF_SERVICE.md', 'out': 'tos.html'},
    {'src': 'PRIVACY_POLICY.md', 'out': 'privacy.html'},
    {'src': 'LEGAL_NOTICE.md', 'out': 'legal-notice.html'},
]

ANCHORS = {}
ESCAPABLE = '\\`*_{}[]()#+-.!|'


def inline(text):
    stash = []

    def keep(s):
        stash.append(s)
        return f'\x00{len(stash) - 1}\x00'

    # Backslash escapes become literal characters before any other markup.
    text = re.sub(r'\\([' + re.escape(ESCAPABLE) + r'])', lambda m: keep(html.escape(m.group(1), quote=False)), text)
    text = re.sub(
        r'\[([^\]]+)\]\(([^)\s]+)\)',
        lambda m: keep(f'<a href="{html.escape(m.group(2))}">{html.escape(m.group(1), quote=False)}</a>'),
        text,
    )
    text = re.sub(
        r'(?<![\w/@.])([\w.+-]+@[\w-]+(?:\.[\w-]+)+)',
        lambda m: keep(f'<a href="mailto:{m.group(1)}">{m.group(1)}</a>'),
        text,
    )
    text = re.sub(
        r'https?://[^\s<>)]*[^\s<>).,;:!?]',
        lambda m: keep(f'<a href="{html.escape(m.group(0))}">{html.escape(m.group(0), quote=False)}</a>'),
        text,
    )

    text = html.escape(text, quote=False)
    text = re.sub(r'\[([A-Z]{2,}[^\]]*)\]', r'<mark class="placeholder">[\1]</mark>', text)
    text = re.sub(r'\*\*(.+?)\*\*', r'<strong>\1</strong>', text)
    text = re.sub(r'(?<![\w*])\*(?!\s)(.+?)(?<!\s)\*(?![\w*])', r'<em>\1</em>', text)

    # "section 4" links to a heading of the same document, but not when it
    # refers to another one ("section 8 of the Terms of Service").
    def section_ref(m):
        nums = re.sub(
            r'\d+',
            lambda n: f'<a href="#{ANCHORS[n.group(0)]}">{n.group(0)}</a>' if n.group(0) in ANCHORS else n.group(0),
            m.group(2),
        )
        return m.group(1) + nums

    text = re.sub(r'\b([Ss]ections? )(\d+(?: (?:and|to) \d+)?)(?!\.?\d)(?! of the)', section_ref, text)
    return re.sub(r'\x00(\d+)\x00', lambda m: stash[int(m.group(1))], text)


def slug(heading):
    heading = re.sub(r'^\d+\.\s+', '', heading)
    return re.sub(r'[^a-z0-9]+', '-', heading.lower()).strip('-')


def table(rows):
    cells = [[c.strip() for c in r.strip().strip('|').split('|')] for r in rows]
    head = cells[0]
    body = [r for r in cells[1:] if not all(re.fullmatch(r':?-+:?', c) for c in r)]
    out = ['<div class="legal-table"><table>', '<thead><tr>']
    out += [f'<th scope="col">{inline(c)}</th>' for c in head]
    out.append('</tr></thead><tbody>')
    for r in body:
        out.append('<tr>' + ''.join(f'<td>{inline(c)}</td>' for c in r) + '</tr>')
    out.append('</tbody></table></div>')
    return '\n'.join(out)


def blocks(lines):
    """Groups lines into (kind, lines) blocks."""
    out, cur, kind = [], [], None

    def flush():
        nonlocal cur, kind
        if cur:
            out.append((kind, cur))
        cur, kind = [], None

    for line in lines:
        if not line.strip():
            flush()
            continue
        if line.startswith('### '):
            flush()
            out.append(('h3', [line[4:].strip()]))
            continue
        k = ('table' if line.startswith('|') else 'ul' if line.startswith('- ')
             else 'ol' if re.match(r'\d+\.\s', line) else 'p')
        if kind and k != kind:
            flush()
        kind = k
        cur.append(line)
    flush()
    return out


def render_block(kind, lines):
    if kind == 'h3':
        return f'<h3 id="{slug(lines[0])}">{inline(lines[0])}</h3>'
    if kind == 'table':
        return table(lines)
    if kind in ('ul', 'ol'):
        items = [re.sub(r'^(- |\d+\.\s)', '', l) for l in lines]
        return f'<{kind}>\n' + '\n'.join(f'    <li>{inline(i)}</li>' for i in items) + f'\n</{kind}>'
    return '<p>' + '<br>\n'.join(inline(l) for l in lines) + '</p>'


def indent(text, n):
    pad = ' ' * n
    return '\n'.join(pad + l if l else l for l in text.split('\n'))


def convert(md, source):
    if '{{' in md or '}}' in md:
        raise SystemExit(f'{source}: "{{{{" and "}}}}" would be read as Go template actions')
    lines = md.strip('\n').split('\n')
    if not lines[0].startswith('# '):
        raise SystemExit(f'{source}: must start with a "# " title')
    title = lines[0][2:].strip()
    rest = lines[1:]
    while rest and not rest[0].strip():
        rest.pop(0)
    meta = rest.pop(0).strip().strip('*').strip()

    ANCHORS.clear()
    for line in rest:
        m = re.match(r'## (\d+)\.', line)
        if m:
            ANCHORS[m.group(1)] = slug(line[3:].strip())

    intro, sections, cur = [], [], None
    for line in rest:
        if line.startswith('## '):
            cur = {'heading': line[3:].strip(), 'lines': []}
            sections.append(cur)
        elif cur is None:
            intro.append(line)
        else:
            cur['lines'].append(line)

    parts = [f'<h1>{inline(title)}</h1>', f'<p class="last-updated">{inline(meta)}</p>']
    if any(l.strip() for l in intro):
        parts.append('\n'.join(render_block(*b) for b in blocks(intro)))
    for s in sections:
        body = '\n'.join(render_block(*b) for b in blocks(s['lines']))
        parts.append(f'<section id="{slug(s["heading"])}">\n    <h2>{inline(s["heading"])}</h2>\n{indent(body, 4)}\n</section>')

    article = '\n\n'.join(parts)
    return (
        f'\n<!-- Generated by scripts/build_legal.py from legal/{source}. Do not edit here. -->\n'
        f'<article class="legal-document">\n{indent(article, 4)}\n</article>\n        '
    )


MAIN = re.compile(r'(<main\b[^>]*>).*?(</main>)', re.S)


def main():
    root = Path(__file__).resolve().parent.parent
    templates = Path(sys.argv[1]) if len(sys.argv) > 1 else root / 'web' / 'templates'
    legal = Path(sys.argv[2]) if len(sys.argv) > 2 else root / 'legal'
    for cfg in PAGES:
        md = (legal / cfg['src']).read_text(encoding='utf-8')
        out = templates / cfg['out']
        tpl = out.read_text(encoding='utf-8')
        if len(MAIN.findall(tpl)) != 1:
            raise SystemExit(f'{out}: expected exactly one <main> element')
        body = convert(md, cfg['src'])
        out.write_text(MAIN.sub(lambda m: m.group(1) + body + m.group(2), tpl), encoding='utf-8')
        left = sorted(set(re.findall(r'\[([A-Z]{2,}[^\]]*)\]|\[(X[^\]]*)\]', md)))
        print(f'{out}: written, {len(left)} placeholder(s) left')


if __name__ == '__main__':
    main()
