#!/usr/bin/env python3
# -*- mode: Python; coding: utf-8 -*-
#

import glob
import gzip
import io
import json
import logging
import os
import time
import statistics
import sys

from PIL import Image
import fontTools.ttLib
from reportlab.pdfgen import canvas
from reportlab.lib.pagesizes import letter, A4
from reportlab.lib.units import inch, mm, cm
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.platypus import Paragraph
#from reportlab.platypus import Image
from reportlab.lib.colors import Color
from reportlab.lib.styles import ParagraphStyle
from reportlab.lib.utils import ImageReader

logger = logging.getLogger(__name__)

class TodoException(Exception):
    pass


class Bfont:
    def __init__(self, path, name=None):
        self.name = name
        self.path = path
        self.capHeightPerPt = None
        self._measureCapheight()
        lfont = TTFont(self.name, self.path)
        pdfmetrics.registerFont(lfont)

    def _measureCapheight(self):
        ftt = fontTools.ttLib.TTFont(self.path)
        caps = [chr(x) for x in range(ord('A'), ord('Z')+1)]
        caps.remove('Q') # outlier descender
        glyfminmax = [(ftt['glyf'][glyc].yMax, ftt['glyf'][glyc].yMin, glyc) for glyc in caps]
        gmaxes = [x[0] for x in glyfminmax]
        gmins = [x[1] for x in glyfminmax]
        capmin = statistics.median(gmins)
        capmax = statistics.median(gmaxes)
        self.capHeightPerPt = (capmax - capmin) / 2048
        if self.name is None:
            for xn in ftt['name'].names:
                if xn.nameID == 4:
                    self.name = xn.toUnicode()
                    break

resources = None
# if this is ballotstudio/draw/draw.py, look in ballotstudio/resources/ or ./resources/
for mayber in (os.path.join(os.getcwd(), 'resources'), os.path.join(os.path.dirname(os.path.dirname(__file__)),'resources')):
    if os.path.isdir(mayber):
        resources = mayber
        break
fonts = {}

def _ensure_fonts():
    if not fonts:
        for fpath in glob.glob('/usr/share/fonts/truetype/liberation/*.ttf') + glob.glob(os.path.join(resources,'*.ttf')):
            xf = Bfont(fpath)
            fonts[xf.name] = xf

        logger.info('fonts: ' + ', '.join([repr(n) for n in fonts.keys()]))

fontsans = 'Liberation Sans'
fontsansbold = 'Liberation Sans Bold'
#fontsans = 'Noto Sans Regular'
#fontsansbold = 'Noto Sans Bold'
# TODO: figure out how to use 亀 etc that aren't in the core font file

class Settings:
    def __init__(self):
        self.headerFontName = fontsansbold
        self.headerFontSize = 14
        self.headerLeading = 15.2
        self.titleFontName = fontsansbold
        self.titleFontSize = 12
        self.titleBGColor = (.85, .85, .85)
        self.titleLeading = self.titleFontSize * 1.4
        self.subtitleFontName = fontsansbold
        self.subtitleFontSize = 12
        self.subtitleBGColor = ()
        self.subtitleLeading = self.subtitleFontSize * 1.4
        self.candidateFontName = fontsansbold
        self.candidateFontSize = 12
        self.candidateLeading = 13
        self.candsubFontName = fontsans
        self.candsubFontSize = 12
        self.candsubLeading = 13
        self.writeInHeight = 0.3 * inch # TODO: check spec
        self.bubbleLeftPad = 0.1 * inch
        self.bubbleRightPad = 0.1 * inch
        self.bubbleWidth = 8 * mm
        self.bubbleMaxHeight = 3 * mm
        self.columnMargin = 0.1 * inch
        self.debugPageOutline = True
        self.nowstrEnabled = True
        self.nowstrFontSize = 10
        self.nowstrFontName = fontsans
        self.pageMargin = 0.5 * inch # inset from paper edge
        self.pagesize = letter


gs = Settings()

contestTitleStyle = ParagraphStyle(
    'contestTitleStyle',
    fontName=fontsansbold,
    fontSize=gs.titleFontSize,
    leading=gs.titleLeading,
    #backColor=Color(*gs.titleBGColor), #Color(0.85,0.85,0.85,1),
    leftIndent=1 + (0.1 * inch),
)

contestSubtitleStyle = ParagraphStyle(
    'contestSubtitleStyle',
    fontName=gs.candsubFontName,
    fontSize=gs.candsubFontSize,
    leading=gs.candsubLeading,
    leftIndent=1 + (0.1 * inch),
)

instructionStyle = ParagraphStyle('instructionParagraph')

selectionStyle = ParagraphStyle(
    'selection',
    fontName=gs.candidateFontName,
    fontSize=gs.candidateFontSize,
    leading=gs.candidateLeading,
)

selsubStyle = ParagraphStyle(
    'selsub',
    fontName=gs.candsubFontName,
    fontSize=gs.candsubFontSize,
    leading=gs.candsubLeading,
)

def setOptionalFields(self, ob):
    for field_name, default_value in self._optional_fields:
        setattr(self, field_name, ob.get(field_name, default_value))


def gpunitName(gpunit):
    name = gpunit.get('Name')
    if name is not None:
        return name
    eis = gpunit.get('ExternalIdentifier')
    if eis:
        return ','.join(eis)
    if gpunit['@type'] == 'ElectionResults.ReportingUnit':
        raise Exception('gpunit with no Name {!r}'.format(gpunit))
    elif gpunit['@type'] == 'ElectionResults.ReportingDevice':
        raise TodoException('TODO: build reporting device name from sub units')
    else:
        raise Exception("unknown gpunit type {}".format(gpunit['@type']))

_votevariation_instruction_en = {
    "approval": "Vote for as many as you like",
    "plurality": "Vote for one",
    "n-of-m": "Vote for up to {VotesAllowed}",
}

class BallotMeasureSelection:
    "NIST 1500-100 v2 ElectionResults.BallotMeasureSelection"
    _optional_fields = (
        ('ExternalIdentifier', []),
        ('SequenceOrder', None), #int
        ('VoteCounts', []), #VoteCounts results objects
    )
    def __init__(self, erctx, cs_json_object):
        self.erctx = erctx
        self.cs = cs_json_object
        self.atid = self.cs['@id']
        self.selection = self.cs['Selection']
        setOptionalFields(self, self.cs)
        self._bubbleCoords = None
    def height(self, width):
        out = gs.candidateLeading
        out += 0.1 * inch
        return out
    def draw(self, c, x, y, width):
        capHeight = fonts[gs.candidateFontName].capHeightPerPt * gs.candidateFontSize
        bubbleHeight = min(3*mm, capHeight)
        bubbleYShim = (capHeight - bubbleHeight) / 2.0
        bubbleBottom = y - gs.candidateFontSize + bubbleYShim
        c.setStrokeColorRGB(0,0,0)
        c.setLineWidth(1)
        rrFill = 0
        if self.erctx.isMarked(self.atid):
            c.setFillColorRGB(0,0,0)
            rrFill = 1
        else:
            c.setFillColorRGB(1,1,1)
        self._bubbleCoords = (x + gs.bubbleLeftPad, bubbleBottom, gs.bubbleWidth, bubbleHeight)
        c.roundRect(*self._bubbleCoords, radius=bubbleHeight/2, fill=rrFill)
        clo = gs.bubbleLeftPad + gs.bubbleWidth + gs.bubbleRightPad
        textx = x + clo #gs.bubbleLeftPad + gs.bubbleWidth + gs.bubbleRightPad
        c.setFillColorRGB(0,0,0)
        cpar = Paragraph(self.selection, selectionStyle)
        ww, wh = cpar.wrap(width - clo, 100)
        cpar.drawOn(c, textx, y-wh)
        ypos = y - wh
        # separator line
        c.setStrokeColorRGB(0,0,0)
        c.setLineWidth(0.25)
        sepy = ypos - (0.1 * inch)
        c.line(textx, sepy, x+width, sepy)
        return

class CandidateSelection:
    "NIST 1500-100 v2 ElectionResults.CandidateSelection"
    _optional_fields = (
        ('CandidateIds', []), #id of Candidate in Election object
        ('EndorsementPartyIds', []), #id of Party or Coalition
        ('IsWriteIn', False), #bool
        ('SequenceOrder', None), #int
        ('VoteCounts', []), #VoteCounts results objects
    )
    def __init__(self, erctx, cs_json_object):
        self.erctx = erctx
        self.cs = cs_json_object
        self.atid = self.cs['@id']
        setOptionalFields(self, self.cs)
        self.candidates = [erctx.getRawOb(cid) for cid in self.CandidateIds]
        self.people = []
        self.peopleparties = []
        for c in self.candidates:
            pid = c.get('PersonId')
            if pid:
                p = erctx.getRawOb(pid)
                self.people.append(p)
                pparty = p.get('PartyId')
                party = pparty and erctx.getRawOb(pparty)
                self.peopleparties.append(party)
            else:
                self.people.append(None)
                self.peopleparties.append(None)
        self.parties = [erctx.getRawOb(x) for x in self.EndorsementPartyIds]
        if self.parties:
            self.subtext = ', '.join([p['Name'] for p in self.parties])
        elif self.people:
            peopleparties = [p['Name'] for p in filter(None, self.peopleparties)]
            self.subtext = ', '.join(peopleparties)
        else:
            self.subtext = None
        self._bubbleCoords = None
    def height(self, width):
        # TODO: actually check render for width with party and subtitle and all that
        # out = gs.candidateLeading * len(self.candidates)
        out = 0
        ballotName = self.ballotName()
        clo = gs.bubbleLeftPad + gs.bubbleWidth + gs.bubbleRightPad
        if ballotName:
            cpar = Paragraph(ballotName, selectionStyle)
            ww, wh = cpar.wrap(width - clo, 100)
            out += wh
        if self.subtext:
            cpar = Paragraph(self.subtext, selsubStyle)
            ww, wh = cpar.wrap(width - clo, 100)
            out += wh
        if self.IsWriteIn:
            out += gs.candsubLeading
            out += gs.writeInHeight
        out += 0.1 * inch
        return out
    def ballotName(self):
        ballotName = None
        if not self.candidates:
            if not self.IsWriteIn:
                ballotName = 'error: no candidates in selection'
        else:
            ballotName = self.candidates[0].get('BallotName')
            if ballotName is None:
                ballotName = 'error: Ballot Name is required in csel for {}'.format(' '.join(self.CandidateIds))
        return ballotName
    def draw(self, c, x, y, width):
        capHeight = fonts[gs.candidateFontName].capHeightPerPt * gs.candidateFontSize
        bubbleHeight = min(3*mm, capHeight)
        bubbleYShim = (capHeight - bubbleHeight) / 2.0
        bubbleBottom = y - gs.candidateFontSize + bubbleYShim
        c.setStrokeColorRGB(0,0,0)
        c.setLineWidth(1)
        rrFill = 0
        if self.erctx.isMarked(self.atid):
            c.setFillColorRGB(0,0,0)
            rrFill = 1
        else:
            c.setFillColorRGB(1,1,1)
        self._bubbleCoords = (x + gs.bubbleLeftPad, bubbleBottom, gs.bubbleWidth, bubbleHeight)
        c.roundRect(*self._bubbleCoords, radius=bubbleHeight/2, fill=rrFill)
        clo = gs.bubbleLeftPad + gs.bubbleWidth + gs.bubbleRightPad
        textx = x + clo # gs.bubbleLeftPad + gs.bubbleWidth + gs.bubbleRightPad
        # TODO: assumes one line
        c.setFillColorRGB(0,0,0)
        ballotName = self.ballotName()
        ypos = y
        if ballotName:
            cpar = Paragraph(ballotName, selectionStyle)
            ww, wh = cpar.wrap(width - clo, 100)
            cpar.drawOn(c, textx, ypos-wh)
            ypos -= wh
        if self.subtext:
            cpar = Paragraph(self.subtext, selsubStyle)
            ww, wh = cpar.wrap(width - clo, 100)
            cpar.drawOn(c, textx, ypos-wh)
            ypos -= wh
        if self.IsWriteIn:
            txto = c.beginText(textx, ypos - gs.candsubFontSize)
            txto.setFont(gs.candsubFontName, gs.candsubFontSize, leading=gs.candsubLeading)
            txto.textLines('write-in:')
            c.drawText(txto)
            ypos -= gs.candsubLeading
            ypos -= gs.writeInHeight
            c.setStrokeColorRGB(0,0,0)
            c.setDash([4,4])
            c.setLineWidth(0.5)
            c.line(textx, ypos, x+width, ypos)
            c.setDash()
        # separator line
        c.setStrokeColorRGB(0,0,0)
        c.setLineWidth(0.25)
        sepy = ypos - (0.1 * inch)
        c.line(textx, sepy, x+width, sepy)
        return

class BallotMeasureContest:
    "NIST 1500-100 v2 ElectionResults.BallotMeasureContest"
    _optional_fields = (
        ('Abbreviation', None), #str
        ('BallotSubTitle', None), #str
        ('BallotTitle', None), #str
        ('ConStatement', None), #str
        ('ContestSelection', []), #[(PartySelection|BallotMeasureSelection|CandidateSelection), ...]
        ('CountStatus', []), #ElectionResults.CountStatus
        ('EffectOfAbstain', None), #str
        ('ExternalIdentifier', []),
        ('FullText', None), #str
        ('HasRotation', False), #bool
        ('InfoUri', []), # []str
        ('OtherCounts', []), #[ElectionResults.OtherCounts, ...]
        ('OtherType', None), #str .Type=other
        ('OtherVoteVariation', []), #str
        ('PassageThreshold', None), #str
        ('ProStatement', None), #str
        ('SequenceOrder', None), #int
        ('SubUnitsReported', None), #int
        ('SummaryText', None), #str
        ('TotalSubUnits', None), #int
        ('Type', None), # ElectionResults.BallotMeasureType {ballot-measure,initiative,recall,referendum,other}
        ('VoteVariation', None), #ElectionResults.VoteVariation
    )
    def __init__(self, erctx, contest_json_object):
        co = contest_json_object
        self.co = co
        self.Name = co['Name']
        self.ElectionDistrictId = co['ElectionDistrictId'] # reference to a ReportingUnit gpunit
        setOptionalFields(self, self.co)
        self.draw_selections = [erctx.makeDrawOb(x) for x in self.ContestSelection]
    def draw(self, c, x, y, width, draw_selections=None):
        if draw_selections is None:
            draw_selections = self.draw_selections
        pos = y - 3 # leave room for 3pt top border
        # title
        tpar = Paragraph(self.BallotTitle, contestTitleStyle)
        ww, wh = tpar.wrap(width, 100)
        c.setStrokeColorRGB(*gs.titleBGColor)
        c.setFillColorRGB(*gs.titleBGColor)
        c.rect(x, pos - wh, width, wh, fill=1, stroke=0)
        tpar.drawOn(c, x, pos-wh)
        pos -= wh
        # subtitle
        c.setStrokeColorCMYK(.1,0,0,0)
        c.setFillColorCMYK(.1,0,0,0)
        c.rect(x, pos - gs.subtitleLeading, width, gs.subtitleLeading, fill=1, stroke=0)
        c.setFillColorRGB(0,0,0)
        c.setStrokeColorRGB(0,0,0)
        # TODO: skip BallotSubTitle if null/empty
        txto = c.beginText(x + 1 + (0.1 * inch), pos - gs.subtitleFontSize)
        txto.setFont(gs.subtitleFontName, gs.subtitleFontSize)
        txto.textLines(self.BallotSubTitle or '')
        c.drawText(txto)
        pos -= gs.subtitleLeading
        c.setFillColorRGB(0,0,0)
        c.setStrokeColorRGB(0,0,0)
        # TODO SummaryText
        pos -= 0.1 * inch # header-choice gap
        maxheight = self._maxheight(width-1)
        for ds in draw_selections:
            dy = ds.height(width)
            ds.draw(c, x+1, pos, width-1)
            pos -= maxheight
        pos -= 0.1 * inch # bottom padding

        # top border
        c.setStrokeColorRGB(0,0,0)
        c.setLineWidth(3)
        c.line(x, y-1.5, x + width, y-1.5) # -0.5 caps left border 1.0pt line
        # left border and bottom border
        c.setLineWidth(1)
        path = c.beginPath()
        path.moveTo(x+0.5, y-1.5)
        path.lineTo(x+0.5, pos-0.5)
        path.lineTo(x+width, pos-0.5)
        c.drawPath(path, stroke=1)
        return
    def _maxheight(self, width, draw_selections=None):
        draw_selections = draw_selections or self.draw_selections
        mh = None
        for ds in draw_selections:
            if getattr(ds, 'IsWriteIn', False):
                continue
            h = ds.height(width)
            if mh is None or h > mh:
                mh = h
        return mh
    def height(self, width, draw_selections=None):
        draw_selections = draw_selections or self.draw_selections
        out = self._maxheight(width-1) * len(draw_selections)
        out += 4 # top and bottom border
        tpar = Paragraph(self.BallotTitle, contestTitleStyle)
        _, wh = tpar.wrap(width, 100)
        out += wh + gs.subtitleLeading
        out += 0.1 * inch # header-choice gap
        out += 0.1 * inch # bottom padding
        return out

class CandidateContest:
    "NIST 1500-100 v2 ElectionResults.CandidateContest"
    _optional_fields = (
        ('Abbreviation', None), #str
        ('BallotSubTitle', None), #str
        ('BallotTitle', None), #str
        ('ContestSelection', []), #[(PartySelection|BallotMeasureSelection|CandidateSelection), ...]
        ('CountStatus', []), #ElectionResults.CountStatus
        ('ExternalIdentifier', []),
        ('HasRotation', False), #bool
        ('NumberElected', None), #int, probably 1
        ('NumberRunoff', None), #int
        ('OfficeIds', []), #[ElectionResults.Office, ...]
        ('OtherCounts', []), #[ElectionResults.OtherCounts, ...]
        ('OtherVoteVariation', []), #str
        ('PrimaryPartyIds', []), #[Party|Coalition, ...]
        ('SequenceOrder', None), #int
        ('SubUnitsReported', None), #int
        ('TotalSubUnits', None), #int
        ('VoteVariation', None), #ElectionResults.VoteVariation
        ('VotesAllowed', None), #int, probably 1
    )
    def __init__(self, erctx, contest_json_object):
        co = contest_json_object
        self.co = co
        self.Name = co['Name']
        self.ElectionDistrictId = co['ElectionDistrictId'] # reference to a ReportingUnit gpunit
        self.VotesAllowed = co['VotesAllowed']
        setOptionalFields(self, self.co)
        if self.OfficeIds:
            self.offices = [erctx.getRawOb(x) for x in self.OfficeIds]
        else:
            self.offices = []
        self.draw_selections = [erctx.makeDrawOb(x) for x in self.ContestSelection]
    def draw(self, c, x, y, width, draw_selections=None):
        if draw_selections is None:
            draw_selections = self.draw_selections
        pos = y - 3 # leave room for 3pt top border
        # title
        tpar = Paragraph(self.BallotTitle, contestTitleStyle)
        ww, wh = tpar.wrap(width, 100)
        c.setStrokeColorRGB(*gs.titleBGColor)
        c.setFillColorRGB(*gs.titleBGColor)
        c.rect(x, pos - wh, width, wh, fill=1, stroke=0)
        tpar.drawOn(c, x, pos-wh)
        pos -= wh
        # subtitle
        c.setStrokeColorCMYK(.1,0,0,0)
        c.setFillColorCMYK(.1,0,0,0)
        c.rect(x, pos - gs.subtitleLeading, width, gs.subtitleLeading, fill=1, stroke=0)
        c.setFillColorRGB(0,0,0)
        c.setStrokeColorRGB(0,0,0)
        txto = c.beginText(x + 1 + (0.1 * inch), pos - gs.subtitleFontSize)
        txto.setFont(gs.subtitleFontName, gs.subtitleFontSize)
        txto.textLines(self.BallotSubTitle)
        c.drawText(txto)
        pos -= gs.subtitleLeading
        pos -= 0.1 * inch # header-choice gap
        c.setFillColorRGB(0,0,0)
        c.setStrokeColorRGB(0,0,0)
        maxheight = self._maxheight(width-1, draw_selections)
        for ds in draw_selections:
            dy = ds.height(width)
            ds.draw(c, x+1, pos, width-1)
            pos -= max(maxheight,dy)
        pos -= 0.1 * inch # bottom padding

        # top border
        c.setStrokeColorRGB(0,0,0)
        c.setLineWidth(3)
        c.line(x, y-1.5, x + width, y-1.5) # -0.5 caps left border 1.0pt line
        # left border and bottom border
        c.setLineWidth(1)
        path = c.beginPath()
        path.moveTo(x+0.5, y-1.5)
        path.lineTo(x+0.5, pos-0.5)
        path.lineTo(x+width, pos-0.5)
        c.drawPath(path, stroke=1)
        return
    def _maxheight(self, width, draw_selections=None):
        "max height of the normal candidates. write-in is different"
        if draw_selections is None:
            draw_selections = self.draw_selections
        mh = None
        for ds in draw_selections:
            if getattr(ds, 'IsWriteIn', False):
                # don't count height of write-in
                continue
            h = ds.height(width)
            if mh is None or h > mh:
                mh = h
        return mh
    def height(self, width, draw_selections=None):
        if draw_selections is None:
            draw_selections = self.draw_selections
        mh = self._maxheight(width-1, draw_selections=draw_selections)
        out = 0
        for ds in draw_selections:
            out += max(mh, ds.height(width))
        out += 4 # top and bottom border
        tpar = Paragraph(self.BallotTitle, contestTitleStyle)
        _, wh = tpar.wrap(width, 100)
        out += wh + gs.subtitleLeading
        out += 0.1 * inch # header-choice gap
        out += 0.1 * inch # bottom padding
        return out

class RetentionContest:
    "NIST 1500-100 v2 ElectionResults.RetentionContest"
    _optional_fields = (
        ('Abbreviation', None), #str
        ('BallotSubTitle', None), #str
        ('BallotTitle', None), #str
        ('ConStatement', None), #str
        ('ContestSelection', []), #[(PartySelection|BallotMeasureSelection|CandidateSelection), ...]
        ('CountStatus', []), #ElectionResults.CountStatus
        ('EffectOfAbstain', None), #str
        ('ExternalIdentifier', []),
        ('HasRotation', False), #bool
        ('InfoUri', []), # []str
        ('OfficeIds', []), #[ElectionResults.Office, ...]
        ('OtherCounts', []), #[ElectionResults.OtherCounts, ...]
        ('OtherType', None), #str .Type=other
        ('OtherVoteVariation', []), #str
        ('PassageThreshold', None), #str
        ('ProStatement', None), #str
        ('SequenceOrder', None), #int
        ('SubUnitsReported', None), #int
        ('SummaryText', None), #str
        ('TotalSubUnits', None), #int
        ('Type', None), # ElectionResults.BallotMeasureType {ballot-measure,initiative,recall,referendum,other}
        ('VoteVariation', None), #ElectionResults.VoteVariation
    )
    def __init__(self, erctx, contest_json_object):
        co = contest_json_object
        self.co = co
        self.Name = co['Name']
        self.CandidateId = co['CandidateId']
        self.ElectionDistrictId = co['ElectionDistrictId'] # reference to a ReportingUnit gpunit
        setOptionalFields(self, self.co)
        self.draw_selections = [erctx.makeDrawOb(x) for x in self.ContestSelection]
        self._title = self.BallotTitle
        if not self._title:
            if self.OfficeIds:
                offices = [erctx.getRawOb(x) for x in self.OfficeIds]
                officeNames = filter(None, [x.get('Name') for x in offices])
                officeName = ', '.join(officeNames)
                self._title = 'Judge Retention:\n{}'.format(officeName)
            else:
                self._title = 'Judge Retention'
    def draw(self, c, x, y, width, draw_selections=None):
        # TODO: fix all copy-pasteos from BallotMeasureContest
        if draw_selections is None:
            draw_selections = self.draw_selections
        pos = y - 3 # leave room for 3pt top border
        # title
        tpar = Paragraph(self._title, contestTitleStyle)
        ww, wh = tpar.wrap(width, 100)
        c.setStrokeColorRGB(*gs.titleBGColor)
        c.setFillColorRGB(*gs.titleBGColor)
        c.rect(x, pos - wh, width, wh, fill=1, stroke=0)
        tpar.drawOn(c, x, pos-wh)
        pos -= wh
        # subtitle, should be "Vote yes or no"
        c.setStrokeColorCMYK(.1,0,0,0)
        c.setFillColorCMYK(.1,0,0,0)
        c.rect(x, pos - gs.subtitleLeading, width, gs.subtitleLeading, fill=1, stroke=0)
        c.setFillColorRGB(0,0,0)
        c.setStrokeColorRGB(0,0,0)
        # TODO: skip BallotSubTitle if null/empty
        txto = c.beginText(x + 1 + (0.1 * inch), pos - gs.subtitleFontSize)
        txto.setFont(gs.subtitleFontName, gs.subtitleFontSize)
        txto.textLines(self.BallotSubTitle or '')
        c.drawText(txto)
        pos -= gs.subtitleLeading
        c.setFillColorRGB(0,0,0)
        c.setStrokeColorRGB(0,0,0)

        # SummaryText: e.g. 'Keep {candidate.ame} as {office.name} of the {gpu.name}'
        if self.SummaryText:
            spar = Paragraph(self.SummaryText, contestSubtitleStyle)
            ww, wh = spar.wrap(width, 100)
            c.setStrokeColorRGB(*gs.titleBGColor)
            c.setFillColorRGB(1,1,1)
            c.rect(x, pos - wh, width, wh, fill=1, stroke=0)
            spar.drawOn(c, x, pos-wh)
            pos -= wh

        pos -= 0.1 * inch # header-choice gap
        maxheight = self._maxheight(width-1)
        for ds in draw_selections:
            dy = ds.height(width)
            ds.draw(c, x+1, pos, width-1)
            pos -= maxheight
        pos -= 0.1 * inch # bottom padding

        # top border
        c.setStrokeColorRGB(0,0,0)
        c.setLineWidth(3)
        c.line(x, y-1.5, x + width, y-1.5) # -0.5 caps left border 1.0pt line
        # left border and bottom border
        c.setLineWidth(1)
        path = c.beginPath()
        path.moveTo(x+0.5, y-1.5)
        path.lineTo(x+0.5, pos-0.5)
        path.lineTo(x+width, pos-0.5)
        c.drawPath(path, stroke=1)
        return
    def _maxheight(self, width, draw_selections=None):
        draw_selections = draw_selections or self.draw_selections
        mh = None
        for ds in draw_selections:
            if getattr(ds, 'IsWriteIn', False):
                continue
            h = ds.height(width)
            if mh is None or h > mh:
                mh = h
        return mh
    def height(self, width, draw_selections=None):
        draw_selections = draw_selections or self.draw_selections
        out = self._maxheight(width-1) * len(draw_selections)
        out += 4 # top and bottom border
        tpar = Paragraph(self._title, contestSubtitleStyle)
        _, wh = tpar.wrap(width, 100)
        out += wh
        out += gs.subtitleLeading
        spar = Paragraph(self.SummaryText, selsubStyle)
        _, wh = spar.wrap(width, 100)
        out += wh
        out += 0.1 * inch # header-choice gap
        out += 0.1 * inch # bottom padding
        return out


class InstructionsHeader:
    header1 = 'Making selections'
    image1 = 'filled bubble.png'
    instruction1 = 'Fill in the oval to the left of the name of your choice. You must blacken the oval completely, and do not make any marks outside of the oval. You do not have to vote in every race.'
    warning1 = 'Do not cross out or erase, or your vote may not count. If you make a mistake or a stray mark, ask for a new ballot from the poll workers.'
    header2 = 'Optional write-in'
    image2 = 'writein.png'
    instruction2 = 'To add a candidate, fill in the oval to the left of “or write-in” and print the name clearly on the dotted line.'

    @classmethod
    def height(self, width, draw_selections=None):
        # y = 0
        # x = 0
        # pos = y - 3 # leave room for 3pt top border

        # textx = x + 1 + (0.1 * inch)
        # availableWidth = width - (1 + (0.1 * inch))
        # bubbleImage = ImageReader(os.path.join(resources, self.image1))
        # imw, imh = bubbleImage.getSize()
        # imHeight = imh * (availableWidth / imw)
        # pos -= imHeight

        # return 3.0 * inch
        h = self._draw(None,0,0,width,draw_selections, enable=False)
        logger.debug("instructions height %r", h)
        return h
    @classmethod
    def draw(self, c, x, y, width, draw_selections=None):
        self._draw(c,x,y,width,draw_selections, enable=True)
    @classmethod
    def _draw(self, c, x, y, width, draw_selections=None, enable=False):
        pos = y - 3 # leave room for 3pt top border
        # title
        if enable:
            c.setStrokeColorRGB(*gs.titleBGColor)
            c.setFillColorRGB(*gs.titleBGColor)
            c.rect(x, pos - gs.titleLeading, width, gs.titleLeading, fill=1, stroke=0)
            c.setFillColorRGB(0,0,0)
            c.setStrokeColorRGB(0,0,0)
            txto = c.beginText(x + 1 + (0.1 * inch), pos - gs.titleFontSize)
            txto.setFont(gs.titleFontName, gs.titleFontSize)
            txto.textLines('Instructions')
            c.drawText(txto)
        pos -= gs.titleLeading

        # TODO: configurable style instead of borrowing candsub style
        textx = x + 1 + (0.1 * inch)
        availableWidth = width - (1 + (0.1 * inch))

        bubbleImage = ImageReader(os.path.join(resources, self.image1))
        imw, imh = bubbleImage.getSize()
        imHeight = imh * (availableWidth / imw)
        if enable:
            c.drawImage(bubbleImage, textx, pos - imHeight, availableWidth, imHeight)
        pos -= imHeight

        i1par = Paragraph(self.instruction1, instructionStyle)
        ww, wh = i1par.wrap(availableWidth, 100)
        if enable:
            i1par.drawOn(c, textx, pos-wh)
        pos -= wh
        # TODO: warning style
        i1par = Paragraph(self.warning1, instructionStyle)
        ww, wh = i1par.wrap(availableWidth, 100)
        if enable:
            i1par.drawOn(c, textx, pos-wh)
        pos -= wh
        pos -= gs.candsubLeading

        writeInIm = ImageReader(os.path.join(resources, self.image2))
        imw, imh = writeInIm.getSize()
        imHeight = imh * (availableWidth / imw)
        if enable:
            c.drawImage(writeInIm, textx, pos - imHeight, availableWidth, imHeight)
        pos -= imHeight

        i1par = Paragraph(self.instruction2, instructionStyle)
        ww, wh = i1par.wrap(availableWidth, 100)
        if enable:
            i1par.drawOn(c, textx, pos-wh)
        pos -= wh

        pos -= 0.1 * inch # bottom padding

        # top border
        if enable:
            c.setStrokeColorRGB(0,0,0)
            c.setLineWidth(3)
            c.line(x, y-1.5, x + width, y-1.5) # -0.5 caps left border 1.0pt line
            # left border and bottom border
            c.setLineWidth(1)
            path = c.beginPath()
            path.moveTo(x+0.5, y-1.5)
            path.lineTo(x+0.5, pos-0.5)
            path.lineTo(x+width, pos-0.5)
            c.drawPath(path, stroke=1)
        return 0-pos

_COLUMN_BREAK_HEIGHT = 999999997
_PAGE_BREAK_HEIGHT = 999999999

class Header:
    "NIST 1500-100 v2 ElectionResults.Header"
    _optional_fields = (
        ('ExternalIdentifier', []),
    )
    def __init__(self, erctx, header_json_object):
        co = header_json_object
        self.co = co
        self.Name = co['Name']
        setOptionalFields(self, self.co)
        self.impl = None
        if self.Name == 'Instructions':
            self.impl = InstructionsHeader
        # TODO: header Name "ColumnBreak" and "PageBreak"
    def height(self, width, draw_selections=None):
        if self.impl:
            return self.impl.height(width, draw_selections)
        if self.Name == 'ColumnBreak':
            return _COLUMN_BREAK_HEIGHT
        if self.Name == 'PageBreak':
            return _PAGE_BREAK_HEIGHT
        return 0
    def draw(self, c, x, y, width, draw_selections=None):
        if self.impl:
            return self.impl.draw(c,x,y,width,draw_selections)

class OrderedContest:
    def __init__(self, erctx, contest_json_object):
        co = contest_json_object
        self.co = co
        self.contest = erctx.getDrawOb(co['ContestId'])
        self.atid = co['ContestId']
        # selection_ids refs by id to PartySelection, BallotMeasureSelection, CandidateSelection; TODO: dereference, where do they come from?
        raw_selections = self.contest.ContestSelection
        selection_ids = co.get('OrderedContestSelectionIds', [])
        # because we might shuffle the candidate presentation order on different ballots:
        if selection_ids:
            self.ordered_selections = [byId(raw_selections, x) for x in selection_ids]
        else:
            self.ordered_selections = raw_selections
        self.draw_selections = [erctx.makeDrawOb(x) for x in self.ordered_selections]
    def _maxheight(self, width):
        return self.contest._maxheight(width, draw_selections=self.draw_selections)
    def height(self, width):
        return self.contest.height(width, draw_selections=self.draw_selections)
    def draw(self, c, x, y, width):
        self.contest.draw(c, x, y, width, draw_selections=self.draw_selections)
        return
    def getBubbles(self):
        return {ch.atid:ch._bubbleCoords for ch in self.draw_selections}

class OrderedHeader:
    def __init__(self, erctx, contest_json_object):
        co = contest_json_object
        self.co = co
        self.header = erctx.getDrawOb(co['HeaderId'])
        self.atid = co['HeaderId']
        # TODO: handle recursive OrderedContest,OrderedHeader entries in subordinate OrderedContent array
    def _maxheight(self, width):
        return self.header._maxheight(width)
    def height(self, width):
        return self.header.height(width)
    def draw(self, c, x, y, width):
        self.header.draw(c, x, y, width)
        return
    def getBubbles(self):
        return None


class BallotStyle:
    def __init__(self, erctx, ballotstyle_json_object):
        try:
            bs = ballotstyle_json_object
            self.bs = bs
            self.erctx = erctx
            self.gpunits = [erctx.getRawOb(x) for x in bs['GpUnitIds']]
            self.ext = bs.get('ExternalIdentifier', [])
            # image_uri is to image of example ballot?
            self.image_uri = bs.get('ImageUri', [])
            self.content = [erctx.makeDrawOb(ob) for ob in bs.get('OrderedContent', [])]
            # e.g. for a party-specific primary ballot (may associate with multiple parties)
            self.parties = [erctx.getRawOb(x) for x in bs.get('PartyIds', [])]
            # _numPages gets filled in on a first rendering pass and used on second pass
            self._numPages = 'X'
            self._pageHeader = bs.get('PageHeader') # extension field
            self._bubbles = None
            self._headerBoxes = {}
            self.contenttop = None
            self.contentbottom = None
            self.contentleft = None
            self.contentright = None
        except Exception as e:
            logger.error('error processing BallotStyle js, %s, %s', e, json.dumps(bs))
            raise
    def select(self, selectors):
        for sel in selectors:
            if sel in self.ext:
                return True
            if sel in self.image_uri:
                return True
        return False
    def pageHeaderText(self, page):
        """Create PageHeader text
        e.g.
        Official Ballot for General Election
        City of Springfield
        Tuesday, November 8, 2022, page 1 of 5
        """
        pht = self.pageHeaderTemplate()
        election = self.erctx.eprinter
        datepart = election.startdate
        if election.startdate != election.enddate:
            datepart += ' - ' + election.enddate
        gpunitnames = ', '.join([gpunitName(x) for x in self.gpunits])
        place = ""
        if self.gpunits:
            place = gpunitName(self.gpunits[-1])
        anydate = election.enddate or election.startdate
        return pht.format(
            PAGE=page,
            PAGES=self._numPages,
            DATE=anydate,
            DATES=datepart,
            PLACES=gpunitnames,
            PLACE=place,
        )
    def pageHeaderTemplate(self):
        if self._pageHeader is not None:
            return self._pageHeader
        return '''General Election, {DATE}
{PLACES} page {PAGE} of {PAGES}'''
    def drawPageHeader(self, c, page):
        c.setStrokeColorRGB(0,0,0)
        c.setLineWidth(1.0)
        c.line(self.contentleft, self.contenttop, self.contentright, self.contenttop)
        txto = c.beginText(self.contentleft + 0.1*inch, self.contenttop - gs.headerFontSize)
        txto.setFont(gs.headerFontName, gs.headerFontSize, gs.headerLeading)
        headerText = self.pageHeaderText(page)
        nlines = len(headerText.splitlines())
        txto.textLines(headerText)
        c.drawText(txto)
        pageHeaderHeight = gs.headerLeading * nlines + 0.1*inch
        #self._pageHeaderHeight = max(pageHeaderHeight, self._pageHeaderHeight)
        box = (self.contentleft + 0.1*inch, self.contenttop,
               self.contentright, self.contenttop - pageHeaderHeight)
        logger.debug('bs (%r) page %s box %r', self.bs['GpUnitIds'], page, box)
        self._headerBoxes[page] = box
        self.contenttop -= pageHeaderHeight

    def name(self):
        return ','.join([gpunitName(gpu) for gpu in self.gpunits])
    def draw(self, c, pagesize):
        widthpt, heightpt = pagesize
        self.contenttop = heightpt - gs.pageMargin
        self.contentbottom = gs.pageMargin
        self.contentleft = gs.pageMargin
        self.contentright = widthpt - gs.pageMargin
        y = self.contenttop
        x = self.contentleft
        page = 1
        if gs.debugPageOutline:
            # draw page outline debug, a red border at content limit
            c.setLineWidth(0.2)
            c.setFillColorRGB(1,1,1)
            c.setStrokeColorRGB(1,.6,.6)
            c.rect(self.contentleft, self.contentbottom, widthpt - (2 * gs.pageMargin), heightpt - (2 * gs.pageMargin), stroke=1, fill=0)
            c.setLineWidth(1)
        nowstr = 'generated ' + time.strftime('%Y-%m-%d %H:%M:%S UTC', time.gmtime())
        c.setTitle('ballot test ' + nowstr)
        if gs.nowstrEnabled:
            c.setFillColorRGB(0,0,0)
            c.setStrokeColorRGB(0,0,0)
            dtw = pdfmetrics.stringWidth(nowstr, gs.nowstrFontName, gs.nowstrFontSize)
            c.setFont(gs.nowstrFontName, gs.nowstrFontSize)
            c.drawString(self.contentright - dtw, self.contentbottom + (gs.nowstrFontSize * 0.2), nowstr)
            self.contentbottom += (gs.nowstrFontSize * 1.2)

        self.drawPageHeader(c, page)
        # TODO: instruction box
        y = self.contenttop

        # (columnwidth * columns) + (gs.columnMargin * (columns - 1)) == width
        columns = 3
        columnwidth = (self.contentright - self.contentleft - (gs.columnMargin * (columns - 1))) / columns
        bubbles = {}
        # content, 2 columns
        colnum = 1
        for xc in self.content:
            height = xc.height(columnwidth)
            if y - height < self.contentbottom:
                # start a new column
                y = self.contenttop
                colnum += 1
                if (colnum > columns) or (height == _PAGE_BREAK_HEIGHT):
                    # start a new page
                    c.showPage()
                    page += 1
                    colnum = 1
                    # reset contenttop for prior header
                    self.contenttop = heightpt - gs.pageMargin
                    # reset contentbottom in case of debug string
                    self.contentbottom = gs.pageMargin
                    self.drawPageHeader(c, page)
                    x = self.contentleft
                    y = self.contenttop
                else:
                    x += columnwidth + gs.columnMargin
            if (height == _COLUMN_BREAK_HEIGHT) or (height == _PAGE_BREAK_HEIGHT):
                # no actual content
                continue
            # TODO: wrap super long issues
            xc.draw(c, x, y, columnwidth)
            y -= height
            y += 1 # bottom border and top border may overlap
            xb = xc.getBubbles()
            if xb:
                #logger.info('xc %r %s bubbles %r', xc, xc.atid, xb)
                #bubbles.append(xb)
                bubbles[xc.atid] = xb
        c.showPage()
        self._numPages = page
        self._bubbles = bubbles
    def getBubbles(self):
        return self._bubbles
    def getHeaderBoxes(self):
        return self._headerBoxes



def gatherIds(ob):
    out = dict()
    _gatherIds(out, ob)
    return out
def _gatherIds(out, ob):
    if isinstance(ob, dict):
        dtype = ob.get('@type')
        did = ob.get('@id')
        if dtype is not None and did is not None:
            if did in out:
                raise Exception('@id collision {!r} for {!r} and {!r}'.format(did, out[did], ob))
            out[did] = ob
        for k, v in ob.items():
            _gatherIds(out, v)
    elif isinstance(ob, (list,tuple)):
        for x in ob:
            _gatherIds(out, x)

CandidateType = 'ElectionResults.Candidate'
CandidateContestType = 'ElectionResults.CandidateContest'
CandidateSelectionType  = 'ElectionResults.CandidateSelection'
ReportingUnitType = 'ElectionResults.ReportingUnit'
HeaderType = 'ElectionResults.Header'
OfficeType = 'ElectionResults.Office'
PartyType = 'ElectionResults.Party'
PersonType = 'ElectionResults.Person'


class ElectionResultsContext:
    "Manage lookup of objects by id, whether raw json/dict or class"
    # map 'Er.Type': func(ctx, json ob)
    _constructors_for_typestrings = {
        'ElectionResults.BallotMeasureContest': BallotMeasureContest,
        'ElectionResults.BallotMeasureSelection': BallotMeasureSelection,
        'ElectionResults.CandidateContest': CandidateContest,
        'ElectionResults.CandidateSelection': CandidateSelection,
        'ElectionResults.OrderedContest': OrderedContest,
        'ElectionResults.OrderedHeader': OrderedHeader,
        'ElectionResults.RetentionContest': RetentionContest,
        'ElectionResults.Header': Header,
        #'ElectionResults.Office': Office,
    }
    def __init__(self, election_results_json_object, eprinter):
        self.er = election_results_json_object
        self.eprinter = eprinter # ElectionPrinter{}
        # obids = {@id: json ob, ...}
        self.obids = gatherIds(self.er)
        # draw objects by id, same key as obids
        self.dobs = {}
        # contestMarkedCsels is the same two level map returned by ballot scanner
        # map[contest @id]map[csel @id](bool marked)
        # For this purpose it can also be cheated skipping the contest part:
        # {"x":{cselId:True, ...}}
        self.contestMarkedCsels = None
    def getRawOb(self, id_string):
        return self.obids[id_string]
    def getDrawOb(self, id_string):
        dob = self.dobs.get(id_string)
        if dob is None:
            rob = self.obids[id_string]
            cf = self._constructors_for_typestrings[rob['@type']]
            dob = cf(self, rob)
            self.dobs[id_string] = dob
        return dob
    def makeDrawOb(self, rob):
        atid = rob.get('@id')
        if atid:
            dob = self.dobs.get(atid)
            if dob:
                return dob
        cf = self._constructors_for_typestrings[rob['@type']]
        dob = cf(self, rob)
        if atid:
            self.dobs[atid] = dob
        return dob
    def isMarked(self, cselId):
        if self.contestMarkedCsels is None:
            return False
        logger.debug('%r in? %r', cselId, self.contestMarkedCsels)
        for mc in self.contestMarkedCsels.values():
            if cselId in mc:
                return True
        return False






# TODO: i18n
_election_types_en = {
    'general': "General Election",
    'partisan-primary-closed': "Primary Election",
    'partisan-primary-open': "Primary Election",
    'primary': "Primary Election",
    'runoff': "Runoff Election",
    'special': "Special Election",
}

class ElectionPrinter:
    def __init__(self, election_report, election):
        # election_report ElectionResults.ElectionReport from json
        # election ElectionResults.Election from json
        er = election_report
        el = election
        erctx = ElectionResultsContext(er, self)
        self.erctx = erctx
        self.er = er
        self.el = el
        self.startdate = el['StartDate']
        self.enddate = el['EndDate']
        self.name = el['Name']
        # election_type in ['general', 'other', 'partisan-primary-closed', 'partisan-primary-open', 'primary', 'runoff', 'special']
        self.election_type = el['Type']
        self.election_type_other = el.get('OtherType')
        self.ext = er.get('ExternalIdentifier', [])
        self.contests = el.get('Contest', [])
        self.candidates = el.get('Candidate', [])
        # ballot_styles is local BallotStyle objects
        self.ballot_styles = []
        for bstyle in el.get('BallotStyle', []):
            self.ballot_styles.append(BallotStyle(erctx,bstyle))
        return
    def setMarks(self, marks):
        "marks is map[contest @id]map[csel @id](bool marked)"
        self.erctx.contestMarkedCsels = marks
    def electionTypeTitle(self):
        # TODO: i18n
        if self.election_type == 'other':
            return self.election_type_other
        return _election_types_en[self.election_type]

    def drawToDir(self, outdir, outname_prefix=None, selectors=None):
        outpaths = []
        _ensure_fonts()
        if outname_prefix is None:
            outname_prefix = self.name + '_'
        for i, bs in enumerate(self.ballot_styles):
            if (selectors is not None) and not bs.select(selectors):
                continue
            names = ','.join([gpunitName(x) for x in bs.gpunits])
            if len(self.ballot_styles) > 1:
                bs_fname = '{}{}_{}.pdf'.format(outname_prefix, i, names)
            else:
                bs_fname = '{}{}.pdf'.format(outname_prefix, names)
            if outdir:
                bs_fname = os.path.join(outdir, bs_fname)
            # dummy draw for pagination
            outdummy = io.BytesIO()
            dc = canvas.Canvas(outdummy, pagesize=gs.pagesize)
            bs.draw(dc, gs.pagesize)
            outdummy = None
            dc = None
            # real draw
            outpaths.append(bs_fname)
            c = canvas.Canvas(bs_fname, pagesize=gs.pagesize) # pageCompression=1
            bs.draw(c, gs.pagesize)
            c.save()
        return outpaths

    def drawToFile(self, outfile=None, selectors=None):
        # TODO: one specific ballot style or all of them to separate PDFs
        _ensure_fonts()
        any = False
        c = canvas.Canvas(outfile, pagesize=gs.pagesize) # pageCompression=1
        for i, bs in enumerate(self.ballot_styles):
            if (selectors is not None) and not bs.select(selectors):
                continue
            any = True
            # dummy draw for pagination
            outdummy = io.BytesIO()
            dc = canvas.Canvas(outdummy, pagesize=gs.pagesize)
            bs.draw(dc, gs.pagesize)
            outdummy = None
            dc = None
            # real draw
            bs.draw(c, gs.pagesize)
        if any:
            c.save()
        else:
            raise Exception('No BallotStyles drawn for selectors {!r}'.format(selectors))
    def getBubbles(self):
        """{
"bsdata": [
  // entry per ballot style
  {
    "GpUnitIds": [str, ...],
    "bubbles": {
      contest id str: {
        selection id str: [left, bottom, width, height], // ...
      }, // ...
    }, // ...
  }, // ...
],
"bubbles": [
  // entry per ballot style
  {
    "contestN": {
      "cselN": [left, bottom, width, height], // ...
    }, // ...
  },
],
}
"""
        # bubbles["bsdata"][ballotStyleIndex]["GpUnitIds"] = [str, ...]
        # bubbles["bsdata"][ballotStyleIndex]["bubbles"][contest id str][selection id str] = [left, bottom, width, height]
        # bubbles["bsdata"][ballotStyleIndex]["headers"][page number str] = [left, top, right, bottom]
        # TODO: fix docstring above to reflect data below
        bsdata = []
        oneheader = None
        for bs in self.ballot_styles:
            headers = bs.getHeaderBoxes()
            for hb in headers.values():
                if oneheader is None:
                    oneheader = hb
                else:
                    if oneheader != hb:
                        # TODO: two-pass system to make them all the same size as the largest header.
                        raise Exception('header box {!r} != {!r}'.format(oneheader, hb))
            ob = {
                'GpUnitIds': bs.bs['GpUnitIds'],
                'bubbles': bs.getBubbles(),
                'headers': headers,
            }
            bsdata.append(ob)
        return {
            'draw_settings': gs.__dict__,
            # bsdata is the way
            'bsdata': bsdata,
            # TODO: deprecate top level 'bubbles' and 'headers'
            'bubbles': [bs.getBubbles() for bs in self.ballot_styles],
            'headers': [bs.getHeaderBoxes() for bs in self.ballot_styles],
        }

# for a list of NIST-1500-100 v2 json/dict objects with "@id" keys, return one
def byId(they, x):
    for y in they:
        if y['@id'] == x:
            return y
    raise KeyError(x)

def bopen(fname, mode='rt'):
    if fname == '-':
        return sys.stdin
    if fname.endswith('.gz'):
        return gzip.open(fname, mode)
    return open(fname, mode)

def main():
    import argparse
    ap = argparse.ArgumentParser()
    ap.add_argument('election_json')
    ap.add_argument('--bubbles', help='path to write bubble json to')
    ap.add_argument('--verbose', default=False, action='store_true')
    ap.add_argument('--outdir', default=None)
    ap.add_argument('--prefix', default='')
    ap.add_argument('--mark', help='bubbles to mark, json from scan.go or randvote.py')
    args = ap.parse_args()
    if args.verbose:
        logging.basicConfig(level=logging.DEBUG)
    else:
        logging.basicConfig(level=logging.INFO)
    if args.election_json:
        fin = bopen(args.election_json)
        er = json.load(fin)
        fin.close()
    marks = None
    if args.mark:
        fin = bopen(args.mark)
        marks = json.load(fin)
        fin.close()

    for el in er.get('Election', []):
        ep = ElectionPrinter(er, el)
        ep.setMarks(marks)
        fnames_written = ep.drawToDir(args.outdir, args.prefix)
        sys.stdout.write(', '.join(fnames_written) + '\n')
        if args.bubbles:
            if args.bubbles == '-':
                bout = sys.stdout
            else:
                bout = open(args.bubbles, 'w')
            json.dump(ep.getBubbles(), bout)
            bout.write('\n')
            bout.close()
    return

if __name__ == '__main__':
    main()
