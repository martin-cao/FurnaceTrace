"""Render the screenshot-based user guide. Requires reportlab and Pillow.

Run with the Codex bundled Python, or a Python environment containing those packages.
"""
from pathlib import Path
import re
from html import escape
from reportlab.lib import colors
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import ParagraphStyle
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.platypus import (
    SimpleDocTemplate, Paragraph, Spacer, Table, TableStyle, Image,
    PageBreak, KeepTogether, CondPageBreak,
)

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / 'docs/user-guide.md'
OUTPUT = ROOT / 'output/pdf/furnace-user-guide.pdf'
OUTPUT.parent.mkdir(parents=True, exist_ok=True)
# Local macOS font; bundle an equivalent CJK TTF when building elsewhere.
FONT = '/System/Library/Fonts/STHeiti Light.ttc'
pdfmetrics.registerFont(TTFont('GuideCJK', FONT, subfontIndex=0))
pdfmetrics.registerFontFamily('GuideCJK', normal='GuideCJK', bold='GuideCJK', italic='GuideCJK', boldItalic='GuideCJK')
TEAL = colors.HexColor('#167365')
INK = colors.HexColor('#203941')
MUTED = colors.HexColor('#627780')
W, H = A4
M = 40
WIDTH = W - 2*M
styles = {
    'body': ParagraphStyle('body', fontName='GuideCJK', fontSize=10, leading=16.5, textColor=INK, spaceAfter=9, wordWrap='CJK'),
    'h2': ParagraphStyle('h2', fontName='GuideCJK', fontSize=20, leading=28, textColor=TEAL, spaceAfter=18, keepWithNext=True, wordWrap='CJK'),
    'h3': ParagraphStyle('h3', fontName='GuideCJK', fontSize=13, leading=20, textColor=TEAL, spaceBefore=10, spaceAfter=8, keepWithNext=True, wordWrap='CJK'),
    'cell': ParagraphStyle('cell', fontName='GuideCJK', fontSize=9.3, leading=14.5, textColor=INK, wordWrap='CJK'),
    'cap': ParagraphStyle('cap', fontName='GuideCJK', fontSize=9, leading=14, textColor=MUTED, spaceAfter=12, wordWrap='CJK'),
    'code': ParagraphStyle('code', fontName='GuideCJK', fontSize=8.2, leading=13, textColor=INK, backColor=colors.HexColor('#edf4f3'), borderPadding=9, spaceAfter=12),
}

def inline(s):
    s = escape(s)
    s = re.sub(r'`([^`]+)`', r'<font color="#167365">\1</font>', s)
    s = re.sub(r'\*\*([^*]+)\*\*', r'<b>\1</b>', s)
    def link(m):
        title, url = m.groups()
        if url.startswith('http'):
            return f'<link href="{url}" color="#167365">{title}</link>'
        return title
    return re.sub(r'\[([^]]+)\]\(([^)]+)\)', link, s)

def paragraph(s, style='body'):
    return Paragraph(inline(s), styles[style])

def footer(c, doc):
    c.setStrokeColor(colors.HexColor('#dae5e6'))
    c.line(M, H-30, W-M, H-30)
    c.setFont('GuideCJK', 8)
    c.setFillColor(MUTED)
    c.drawString(M, H-23, '炉前 / 入炉追溯系统操作手册')
    c.drawRightString(W-M, H-23, '2026-09-21 · 模拟演示版')
    c.drawString(M, 24, '实际运行截图 · FUXA / MQTT / RTSP')
    c.drawRightString(W-M, 24, str(doc.page))

story=[]
# Cover and navigation, then one chapter per new page.
story.extend([Spacer(1,45), paragraph('炉前 · 入炉追溯', 'h2'), Spacer(1,8)])
story.append(Paragraph('用户操作手册', ParagraphStyle('cover',parent=styles['h2'],fontSize=32,leading=44)))
story.append(paragraph('功能说明 / 界面导览 / 完整流程 / 答辩演示'))
story.append(Spacer(1,20))
story.append(Image(str(ROOT/'docs/screenshots/03-fuxa.png'),width=WIDTH,height=WIDTH*720/1280))
story.append(Spacer(1,18))
story.append(paragraph('本手册面向管理员、普通用户与答辩演示人员。截图来自本机实际运行；设备、摄像头画面与 MES 数据使用模拟输入。'))
story.append(paragraph('两个操作入口：业务工作台 localhost:31080；FUXA 现场页面 localhost:31081。手动设温在 FUXA，报警阈值配置在工作台。'))
story.append(paragraph('阅读顺序：功能与登录（1–2）→ 工作台与炉温（3–5）→ 报警、配对（6–7）→ 入炉、追溯与异常（8–10）→ 演示、启停及边界（11–13）。'))

lines=SOURCE.read_text().splitlines()
i=0
while i<len(lines):
    line=lines[i].strip()
    if line.startswith('## '):
        story.append(PageBreak()); story.append(paragraph(line[3:],'h2')); i+=1; break
    i+=1
while i<len(lines):
    line=lines[i].strip()
    if not line:
        i+=1; continue
    if line.startswith('## '):
        story.append(CondPageBreak(600));story.append(Spacer(1,16));story.append(paragraph(line[3:],'h2'));i+=1;continue
    if line.startswith('### '):
        story.append(paragraph(line[4:],'h3'));i+=1;continue
    if line.startswith('```'):
        code=[];i+=1
        while i<len(lines) and not lines[i].startswith('```'):
            code.append(lines[i]);i+=1
        # Long paths wrap as prose; shell commands keep their actual text in Markdown.
        for row in code:
            story.append(paragraph(row or ' ', 'code'))
        i+=1;continue
    if line.startswith('|'):
        rows=[]
        while i<len(lines) and lines[i].strip().startswith('|'):
            row=[c.strip() for c in lines[i].strip().strip('|').split('|')]
            if not all(re.fullmatch(r'[-: ]+',c) for c in row): rows.append(row)
            i+=1
        n=len(rows[0])
        if n==2: widths=[WIDTH*.27,WIDTH*.73]
        elif n==3:
            widths=[WIDTH*.22,WIDTH*.39,WIDTH*.39]
            if rows[0][0]=='顺序': widths=[WIDTH*.10,WIDTH*.48,WIDTH*.42]
            if rows[0][0]=='操作': widths=[WIDTH*.58,WIDTH*.21,WIDTH*.21]
        else: widths=[WIDTH/n]*n
        data=[[paragraph(c,'cell') for c in row] for row in rows]
        table=Table(data,colWidths=widths,repeatRows=1,hAlign='LEFT')
        table.setStyle(TableStyle([
            ('BACKGROUND',(0,0),(-1,0),colors.HexColor('#dceee9')),
            ('ROWBACKGROUNDS',(0,1),(-1,-1),[colors.white,colors.HexColor('#f3f7f7')]),
            ('VALIGN',(0,0),(-1,-1),'TOP'),('LEFTPADDING',(0,0),(-1,-1),9),('RIGHTPADDING',(0,0),(-1,-1),9),
            ('TOPPADDING',(0,0),(-1,-1),8),('BOTTOMPADDING',(0,0),(-1,-1),8),
            ('LINEBELOW',(0,0),(-1,0),.7,colors.HexColor('#a9c9c0')),
        ]))
        story.extend([table,Spacer(1,12)]);continue
    m=re.fullmatch(r'!\[([^]]*)\]\(([^)]+)\)',line)
    if m:
        label,path=m.groups()
        picture=Image(str(SOURCE.parent/path))
        maxheight = 240 if path.endswith('01-login.png') else 310 if path.endswith('08-notification-preferences.png') else 470
        scale=min(WIDTH/picture.imageWidth,maxheight/picture.imageHeight)
        picture.drawWidth=picture.imageWidth*scale;picture.drawHeight=picture.imageHeight*scale
        story.append(KeepTogether([picture,Spacer(1,6),paragraph(label,'cap')]))
        i+=1;continue
    if line.startswith('- '): line='• '+line[2:]
    story.append(paragraph(line));i+=1

SimpleDocTemplate(str(OUTPUT),pagesize=A4,rightMargin=M,leftMargin=M,topMargin=49,bottomMargin=43,
                  title='炉前 · 入炉追溯系统操作手册',author='炉前项目',pageCompression=1).build(story,onFirstPage=footer,onLaterPages=footer)
print(OUTPUT)
