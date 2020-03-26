#!/usr/bin/env python3
# -*- mode: Python; coding: utf-8 -*-
#

import glob
import json
import logging
import time
import statistics
import sys

import fontTools.ttLib
from reportlab.pdfgen import canvas
from reportlab.lib.pagesizes import letter, A4
from reportlab.lib.units import inch, mm, cm
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
#from reportlab.platypus import Paragraph
#from reportlab.lib.units import ParagraphStyle

import demorace

logger = logging.getLogger(__name__)

def bubble(c, width=12*mm, height=4*mm, r=1*mm):
    pth = c.beginPath()
    pth.roundRect(0, 0, width, height, r)
    return pth


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


fonts = {}

for fpath in glob.glob('/usr/share/fonts/truetype/liberation/*.ttf'):
    xf = Bfont(fpath)
    fonts[xf.name] = xf

#print('fonts: ' + ', '.join([repr(n) for n in fonts.keys()]))


class Settings:
    def __init__(self):
        self.titleFontName = 'Liberation Sans Bold'
        self.titleFontSize = 12
        self.titleBGColor = (.85, .85, .85)
        self.titleLeading = self.titleFontSize * 1.4
        self.subtitleFontName = 'Liberation Sans Bold'
        self.subtitleFontSize = 12
        self.subtitleBGColor = ()
        self.subtitleLeading = self.subtitleFontSize * 1.4
        self.candidateFontName = 'Liberation Sans Bold'
        self.candidateFontSize = 12
        self.candidateLeading = 13
        self.candsubFontName = 'Liberation Sans'
        self.candsubFontSize = 12
        self.candsubLeading = 13
        self.bubbleLeftPad = 0.1 * inch
        self.bubbleRightPad = 0.1 * inch
        self.bubbleWidth = 8 * mm
        self.bubbleMaxHeight = 3 * mm
        self.columnMargin = 0.1 * inch
        self.debugPageOutline = True
        self.nowstrEnabled = True
        self.nowstrFontSize = 10
        self.nowstrFontName = 'Liberation Sans'
        self.pageMargin = 0.5 * inch # inset from paper edge


gs = Settings()


class Choice:
    def __init__(self, name, subtext=None):
        self.name = name
        self.subtext = subtext
        # TODO: measure text for box width & wrap. see reportlab.platypus.Paragraph
        # TODO: wrap with optional max-5% squish instead of wrap
        self._bubbleCoords = None
    def height(self):
        # TODO: multiline for name and subtext
        y = 0
        ypos = y - gs.candidateLeading
        if self.subtext:
            ypos -= gs.candsubLeading
        return -1*(ypos - (0.1 * inch))
    def draw(self, c, x, y, width=(7.5/2)*inch - 1):
        # x,y is a top,left of a box to draw bubble and text into
        capHeight = fonts[gs.candidateFontName].capHeightPerPt * gs.candidateFontSize
        bubbleHeight = min(3*mm, capHeight)
        bubbleYShim = (capHeight - bubbleHeight) / 2.0
        bubbleBottom = y - gs.candidateFontSize + bubbleYShim
        c.setStrokeColorRGB(0,0,0)
        c.setLineWidth(1)
        c.setFillColorRGB(1,1,1)
        self._bubbleCoords = (x + gs.bubbleLeftPad, bubbleBottom, gs.bubbleWidth, bubbleHeight)
        c.roundRect(*self._bubbleCoords, radius=bubbleHeight/2)
        textx = x + gs.bubbleLeftPad + gs.bubbleWidth + gs.bubbleRightPad
        # TODO: assumes one line
        c.setFillColorRGB(0,0,0)
        txto = c.beginText(textx, y - gs.candidateFontSize)
        txto.setFont(gs.candidateFontName, gs.candidateFontSize, gs.candidateLeading)
        txto.textLines(self.name)
        c.drawText(txto)
        ypos = y - gs.candidateLeading
        if self.subtext:
            txto = c.beginText(textx, ypos - gs.candsubFontSize)
            txto.setFont(gs.candsubFontName, gs.candsubFontSize, leading=gs.candsubLeading)
            txto.textLines(self.subtext)
            c.drawText(txto)
            ypos -= gs.candsubLeading
        # separator line
        c.setStrokeColorRGB(0,0,0)
        c.setLineWidth(0.25)
        sepy = ypos - (0.1 * inch)
        c.line(textx, sepy, x+width, sepy)
        return
    def _writeInLine(self, c):
        c.setDash([4,4])
        c.setLineWidth(0.5)
        c.setDash(None)


class Contest:
    def __init__(self, name, title=None, subtitle=None, choices=None):
        self.name = name
        self.title = title or name
        self.subtitle = subtitle
        self.choices = choices
        self._choices_height = None
        self._height = None
        self._maxChoiceHeight = None
    def height(self):
        if self._height is None:
            choices = self.choices or []
            self._maxChoiceHeight = max([x.height() for x in choices])
            ch = self._maxChoiceHeight * len(choices)
            ch += 4 # top and bottom border
            ch += gs.titleLeading + gs.subtitleLeading
            ch += 0.1 * inch # header-choice gap
            ch += 0.1 * inch # bottom padding
            self._height = ch
        return self._height
    def draw(self, c, x, y, width=(7.5/2)*inch - 1):
        # x,y is a top,left
        height = self.height()

        pos = y - 1.5
        # title
        c.setStrokeColorRGB(*gs.titleBGColor)
        c.setFillColorRGB(*gs.titleBGColor)
        c.rect(x, pos - gs.titleLeading, width, gs.titleLeading, fill=1, stroke=0)
        c.setFillColorRGB(0,0,0)
        c.setStrokeColorRGB(0,0,0)
        txto = c.beginText(x + 1 + (0.1 * inch), pos - gs.titleFontSize)
        txto.setFont(gs.titleFontName, gs.titleFontSize)
        txto.textLines(self.title)
        c.drawText(txto)
        pos -= gs.titleLeading
        # subtitle
        c.setStrokeColorCMYK(.1,0,0,0)
        c.setFillColorCMYK(.1,0,0,0)
        c.rect(x, pos - gs.subtitleLeading, width, gs.subtitleLeading, fill=1, stroke=0)
        c.setFillColorRGB(0,0,0)
        c.setStrokeColorRGB(0,0,0)
        txto = c.beginText(x + 1 + (0.1 * inch), pos - gs.subtitleFontSize)
        txto.setFont(gs.subtitleFontName, gs.subtitleFontSize)
        txto.textLines(self.subtitle)
        c.drawText(txto)
        pos -= gs.subtitleLeading
        pos -= 0.1 * inch # header-choice gap
        c.setFillColorRGB(0,0,0)
        c.setStrokeColorRGB(0,0,0)
        choices = self.choices or []
        for ch in choices:
            ch.draw(c, x + 1, pos, width=width - 1)
            pos -= self._maxChoiceHeight

        # top border
        c.setStrokeColorRGB(0,0,0)
        c.setLineWidth(3)
        c.line(x-0.5, y, x + width, y) # -0.5 caps left border 1.0pt line
        # left border and bottom border
        c.setLineWidth(1)
        path = c.beginPath()
        path.moveTo(x, y)
        path.lineTo(x, y-height)
        path.lineTo(x+width, y-height)
        c.drawPath(path, stroke=1)
        return
    def getBubbles(self):
        choices = self.choices or []
        return {ch.name:ch._bubbleCoords for ch in choices}



therace = Contest(
    'Everything', 'The Race for Everything', 'Choose as many as you like',
    [
        Choice('Alice Argyle', 'Anklebiter Assembly'),
        Choice('Bob Brocade', 'Boring Board'),
        Choice('Çandidate Ñame 亀', 'Cowardly Coalition'),
        Choice('Dorian Duck', 'Dumb Department'),
        Choice('Elaine Entwhistle', 'Electable Entertainers'),
    ],
)

raceZ = Contest(
    'Nothing', 'The Race To The Bottom', 'Vote for one',
    [
        Choice('Zaphod', "He's just this guy, you know?"),
        Choice('Zardoz', 'There can be only one'),
        Choice('Zod', 'Kneel'),
    ],
)

headDwarfRace = Contest(
    'Head Dwarf',
    'Head Dwarf',
    'Vote for one',
    [
        Choice('Sleepy'),
        Choice('Happy'),
        Choice('Dopey'),
        Choice('Grumpy'),
        Choice('Sneezy'),
        Choice('Bashful'),
        Choice('Doc'),
    ],
)

# maxChoiceHeight = max([x.height() for x in choices])
# pos = heightpt - 0.5*inch # - pointsize * 1.2
# for ch in choices:
#     ch.draw(c, 0.5*inch, pos)
#     pos -= maxChoiceHeight

# # case-insensitive dict.pop()
# def cp(key, d, exc=True):
#     if key in d:
#         return d.pop(key)
#     kl = key.lower()
#     for dk in d.keys():
#         dkl = dk.lower()
#         if dkl == kl:
#             return d.pop(dk)
#     if exc:
#         raise KeyError(key)
#     return None

# # multi-option case-insensitive dict.pop()
# def ocp(d, *keys, default=None, exc=True):
#     for k in keys:
#         try:
#             return cp(k, d)
#         except:
#             pass
#     if default is not None:
#         return default
#     if exc:
#         raise KeyError(key)
#     return default

# def maybeToDict(o):
#     if isinstance(o, list):
#         return [maybeToDict(ox) for ox in o]
#     elif isinstance(o, dict):
#         return {k:maybeToDict(v) for k,v in o.items()}
#     elif hasattr(o, 'toDict'):
#         return o.toDict()
#     return o

# class Builder:
#     def toDict(self):
#         d = {}
#         if hasattr(self, '_type'):
#             d['@type'] = self._type
#         if hasattr(self, '_id'):
#             d['@id'] = self._id
#         for k,v in self.__dict__.items():
#             if k[0] != '_' and not hasattr(v, '__call__'):
#                 d[k] = maybeToDict(v)

# class Person(Builder):
#     _fields = ()
#     _type = "ElectionResults.Person"
#     def __init__(self):
#         pass
# class ElectionReportBuilder:
#     _type = "ElectionResults.ElectionReport"
#     def __init__(self, **kwargs):
#         # required
#         self.Format = ocp(kwargs, "Format", default="summary-contest")
#         self.GeneratedDate = ocp(kwargs, "GeneratedDate", "date", default=time.strftime("%Y-%m-%d %H:%M:%S %z", time.localtime()))
#         self.Issuer = ocp(kwargs, "Issuer", default="bolson")
#         self.IssuerAbbreviation = ocp(kwargs, "IssuerAbbreviation", default=self.Issuer)
#         self.SequenceStart = int(ocp(kwargs, "SequenceStart", default=1))
#         self.SequenceEnd = int(ocp(kwargs, "SequenceEnd", default=1))
#         self.Status = ocp(kwargs, "Status", default="pre-election")
#         self.VendorApplicationId = ocp(kwargs, "VendorApplicationId", default="BallotGen 0.0.1")
#         # etc
#         self.Election = []
#         self.ExternalIdentifier = []
#         self.Header = []
#         self.IsTest = True
#         self.Notes = ""
#         self.Office = []
#         self.OfficeGroup = []
#         self.Party = []
#         self.Person = []
#         self.TestType = ""
#     def Person(self, **kwargs):
#         pass

def gpunitName(gpunit):
    if gpunit['@type'] == 'ElectionResults.ReportingUnit':
        name = gpunit.get('Name')
        if name is not None:
            return name
        raise Exception('gpunit with no Name {!r}'.format(gpunit))
    elif gpunit['@type'] == 'ElectionResults.ReportingDevice':
        raise Exception('TODO: build reporting device name from sub units')
    else:
        raise Exception("unknown gpunit type {}".format(gpunit['@type']))

_votevariation_instruction_en = {
    "approval": "Vote for as many as you like",
    "plurality": "Vote for one",
    "n-of-m": "Vote for up to {VotesAllowed}",
}

class CandidateSelection:
    def __init__(self, cs_json_object):
        self.cs = cs_json_object
def rehydrateContestSelection(contestselection_json_object):
    cs = contestselection_json_object
    cstype = cs['@type']
    if cstype == 'ElectionResults.CandidateSelection':
        pass
    # TODO ElectionResults.BallotMeasureSelection
    # TODO ElectionResults.PartySelection
    raise Exception('unkown ContestSelection type {!r}'.format(cstype))

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
    def __init__(self, election, contest_json_object):
        co = contest_json_object
        self.co = co
        self.Name = co['Name']
        self.ElectionDistrictId = co['ElectionDistrictId'] # reference to a ReportingUnit gpunit
        self.VotesAllowed = co['VotesAllowed']
        for field_name, default_value in self._optional_fields:
            setattr(self, field_name, co.get(field_name, default_value))
        if self.OfficeIds:
            offices = election.er.get('Office', [])
            self.offices = [byId(offices, x) for x in self.OfficeIds]
        else:
            self.offices = []
    def draw(self, c):
        pass

def rehydrateContest(election, contest_json_object):
    co = contest_json_object
    cotype = co['@type']
    if cotype == 'ElectionResults.CandidateContest':
        return CandidateContest(election, contest_json_object)
    elif cotype == 'ElectionResults.BallotMeasureContest':
        raise Exception('TODO: implement contest type {}'.format(cotype))
    elif cotype == 'ElectionResults.PartyContest':
        raise Exception('TODO: implement contest type {}'.format(cotype))
    elif cotype == 'ElectionResults.RetentionContest':
        raise Exception('TODO: implement contest type {}'.format(cotype))
    else:
        raise Exception('unknown contest type {!r}'.format(cotype))

class OrderedContest:
    def __init__(self, election, contest_json_object):
        "election is local ElectionPrinter()"
        co = contest_json_object
        self.co = co
        cjo = byId(election.contests, co['ContestId'])
        self.contest = rehydrateContest(election, cjo)
        # selection_ids refs by id to PartySelection, BallotMeasureSelection, CandidateSelection; TODO: dereference, where do they come from?
        raw_selections = self.contest.ContestSelection
        selection_ids = co.get('OrderedContestSelectionIds', [])
        # because we might shuffle the candidate presentation order on different ballots:
        if selection_ids:
            self.ordered_selections = [byId(raw_selections, x) for x in selection_ids]
        else:
            self.ordered_selections = raw_selections
    def height(self, width):
        return None


class OrderedHeader:
    "Header with nested content"
    def __init__(self, election, header_json_object):
        "election is local ElectionPrinter()"
        h = header_json_object
        self.h = h
        self.header = byId(TODO.headers, h['@id'])
        self.content = rehydrateOrderedContent(election, h.get('OrderedContent', []))

def rehydrateOrderedContent(election, they):
    "election is local ElectionPrinter()"
    return list(rehydrateOrderedContent_inner(election, they))
def rehydrateOrderedContent_inner(election, they):
    for co in they:
        co_type = co['@type']
        if co_type == 'ElectionResults.OrderedContest':
            yield OrderedContest(election, co)
        elif co_type == '':
            yield OrderedHeader(election, co)
        else:
            raise Exception('unknown ordered content element type={!r}'.format(co_type))

class BallotStyle:
    def __init__(self, election_report, election, ballotstyle_json_object):
        # election_report ElectionResults.ElectionReport
        # election ElectionPrinter()
        # ballotstyle_json_object ElectionResults.BallotStyle
        er = election_report
        gpunits = er.get('GpUnit', [])
        parties = er.get('Party', [])
        bs = ballotstyle_json_object
        self.bs = bs
        self.gpunits = [byId(gpunits, x) for x in bs['GpUnitIds']]
        self.ext = bs.get('ExternalIdentifier', [])
        # image_uri is to image of example ballot?
        self.image_uri = bs.get('ImageUri', [])
        self.content = rehydrateOrderedContent(election, bs.get('OrderedContent', []))
        # e.g. for a party-specific primary ballot (may associate with multiple parties)
        self.parties = [byId(parties, x) for x in bs.get('PartyIds', [])]
        # _numPages gets filled in on a first rendering pass and used on second pass
        self._numPages = None
        self._pageHeader = None
        self._bubbles = None
    def pageHeader(self):
        """e.g.
        Official Ballot for General Election
        City of Springfield
        Tuesday, November 8, 2022
        """
        if self._pageHeader is not None:
            return self._pageHeader
        datepart = self.election.startdate
        if self.election.startdate != self.election.enddate:
            datepart += ' - ' + self.election.enddate
        gpunitnames = ', '.join([gpunitName(x) for x in self.gpunits])
        text = "Ballot for {}\n{}\n{}".format(
            self.election.electionTypeTitle(), gpunitnames, datepart)
        self._pageHeader = text
        return self._pageHeader
    def draw(self, c, pagesize):
        widthpt, heightpt = pagesize
        contenttop = heightpt - gs.pageMargin
        contentbottom = gs.pageMargin
        contentleft = gs.pageMargin
        contentright = widthpt - gs.pageMargin
        y = contenttop
        page = 1
        if gs.debugPageOutline:
            # draw page outline debug
            c.setLineWidth(0.2)
            c.setFillColorRGB(1,1,1)
            c.setStrokeColorRGB(1,.6,.6)
            c.rect(contentleft, contentbottom, widthpt - (2 * gs.pageMargin), heightpt - (2 * gs.pageMargin), stroke=1, fill=0)
            c.setLineWidth(1)
        nowstr = 'generated ' + time.strftime('%Y-%m-%d %H:%M:%S UTC', time.gmtime())
        c.setTitle('ballot test ' + nowstr)
        if gs.nowstrEnabled:
            dtw = pdfmetrics.stringWidth(nowstr, gs.nowstrFontName, gs.nowstrFontSize)
            c.setFont(gs.nowstrFontName, gs.nowstrFontSize)
            c.drawString(contentright - dtw, contentbottom + (gs.nowstrFontSize * 0.2), nowstr)
            contentbottom += (gs.nowstrFontSize * 1.2)

        # TODO: real instruction box instead of fake
        height = 2.9 * inch
        c.setStrokeColorRGB(0,0,0)
        c.rect(contentleft, y - height, contentright - contentleft, height, stroke=1, fill=0)
        c.drawString(contentleft, y - 0.3*inch, 'instruction text here, etc.')
        y -= height

        # (columnwidth * columns) + (gs.columnMargin * (columns - 1)) == width
        columns = 2
        columnwidth = (contentright - contentleft - (gs.columnMargin * (columns - 1))) / columns
        x = contentleft
        bubbles = []
        # content, 2 columns
        colnum = 1
        for xc in self.content:
            height = xc.height(columnwidth)
            if y - height < contentbottom:
                y = contenttop
                colnum += 1
                if colnum > columns:
                    c.showPage()
                    page += 1
                    # TODO: page headers
                    colnum = 1
                    x = contentleft
                    y = contenttop
                else:
                    x += columnwidth + gs.columnMargin
            # TODO: wrap super long issues
            xc.draw(c, x, y, columnwidth)
            y -= height
            xb = xc.getBubbles()
            if xb is not None:
                bubbles.append(xb)
        c.showPage()
        c.save()
        self._numPages = page
        self._bubbles = bubbles


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
        self.er = er
        self.el = el
        # resources referred to:
        gpunits = er.get('GpUnit', [])
        headers = er.get('Header', [])
        offices = er.get('Office', [])
        parties = er.get('Party', [])
        people = er.get('Person', [])
        # load data
        self.scope_gpunit = byId(gpunits, el['ElectionScopeId'])
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
            self.ballot_styles.append(BallotStyle(er, self,bstyle))
        return
    def electionTypeTitle(self):
        # TODO: i18n
        if self.election_type == 'other':
            return self.election_type_other
        return _election_types_en[self.election_type]

    def draw(self, outdir='.', outname_prefix=''):
        # TODO: one specific ballot style or all of them to separate PDFs
        for i, bs in enumerate(self.ballot_styles):
            names = ','.join([gpunitName(x) for x in bs.gpunits])
            if len(self.ballot_styles) > 1:
                bs_fname = '{}{}_{}.pdf'.format(outname_prefix, i, names)
            else:
                bs_fname = '{}{}.pdf'.format(outname_prefix, names)
            c = canvas.Canvas('/tmp/a.pdf', pagesize=letter) # pageCompression=1
            bs.draw(c, letter)
        pass

#demorace.ElectionReport
# TODO: print ballot from ElectionReport.Election[0].BallotStyle[0]...
#json.dump(demorace.ElectionReport, sys.stdout, indent=2)
#sys.stdout.write('\n')

# for a list of NIST-1500-100 v2 json/dict objects with "@id" keys, return one
def byId(they, x):
    for y in they:
        if y['@id'] == x:
            return y
    raise KeyError(x)

def old():
    c = canvas.Canvas('/tmp/a.pdf', pagesize=letter) # pageCompression=1
    widthpt, heightpt = letter

    nowstr = 'generated ' + time.strftime('%Y-%m-%d %H:%M:%S UTC', time.gmtime())
    nowstrFontSize = 12
    mainfontname = 'Liberation Sans'
    dtw = pdfmetrics.stringWidth(nowstr, mainfontname, nowstrFontSize)
    c.setFont(mainfontname, nowstrFontSize)
    c.drawString(widthpt - 0.5*inch - dtw, heightpt - 0.5*inch - nowstrFontSize*1.2, nowstr)
    c.setTitle('ballot test ' + nowstr)

    if False:
        # draw page outline debug
        c.setFillColorRGB(1,1,1)
        c.setStrokeColorRGB(1,.6,.6)
        c.rect(0.5 * inch, 0.5 * inch, widthpt - (1.0 * inch), heightpt - (1.0 * inch), stroke=1, fill=0)

    c.setStrokeColorRGB(0,0,0)
    c.rect(0.5 * inch, heightpt - 3.4 * inch, widthpt - 1.0 * inch, 2.9 * inch, stroke=1, fill=0)
    c.drawString(0.7*inch, heightpt - 0.8*inch, 'instruction text here, etc.')

    races = [therace, headDwarfRace, raceZ]
    x = 0.5 * inch
    top = heightpt - 0.5*inch - 3*inch
    y = top
    bottom = 0.5 * inch
    colwidth = (7.5/2)*inch - gs.columnMargin
    for xr in races:
        height = xr.height()
        if y - height < bottom:
            y = top
            x += colwidth + gs.columnMargin
            # TODO: check for next-page
        xr.draw(c, x, y, colwidth)
        y -= xr.height()
    #therace.draw(c,x, y)
    #y -= therace.height()
    #race2.draw(c, 0.5 * inch, y)

    c.showPage()
    c.save()

    bd = {'bubbles': therace.getBubbles()}
    print(json.dumps(bd))
    return

def main():
    logging.basicConfig(level=logging.INFO)
    er = demorace.ElectionReport
    for el in er.get('Election', []):
        ep = ElectionPrinter(er, el)
        ep.draw()
    return

if __name__ == '__main__':
    main()
