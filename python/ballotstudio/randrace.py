#!/usr/bin/env python3

import logging
import os
import random
import re
import time

if __name__ == '__main__':
    # This is a dumb hack only relevent for debugging as `python3 randrace.py`
    if __package__ is None:
        import sys
        sys.path = [os.path.dirname(os.path.dirname(__file__))] + sys.path
        __package__ = 'draw'

from . import demorace
#from demorace import Sequences
Sequences = demorace.Sequences
import collections

logger = logging.getLogger(__name__)

_names = None
_words = None

def names():
    if not _names:
        loadnames()
    return _names

def randomName():
    return random.choice(names())

def words():
    if not _words:
        loadnames()
    return _words

def randomWordString(n=5):
    return ' '.join(random.sample(words(), n))

def loadnames():
    global _names
    global _words
    namef = re.compile(r'^[A-Z][a-zA-Z]+$')
    wf = re.compile(r'^[a-z]+$')
    words = []
    names = []
    for path in ('/usr/share/dict/words',): # TODO: other paths?
        if not os.path.exists(path):
            continue
        with open(path) as fin:
            for line in fin:
                if not line:
                    continue
                line = line.strip()
                if not line:
                    continue
                if namef.match(line):
                    names.append(line)
                elif wf.match(line):
                    words.append(line)
        logger.debug('%s: %d names %d words', path, len(names), len(words))
        if names and words:
            _names = names
            _words = words
            return


contestMechanic = collections.namedtuple('contestMechanic', ('name', 'data', 'fn'))

contestMechanics = []
def makeContestMechanic(name, data, modfn):
    #contestMechanics[name] = contestMechanic(name, data, modfn)
    contestMechanics.append(contestMechanic(name, data, modfn))

# contestMechanic fn is (RandElection, dict) where dict is the contest JSON

def approvalContestModf(relec, cont):
    cont["ContestSelection"] = [relec.cselForCandidate(relec.makeCandidate()) for i in range(relec.nCandidates())]
    cont["VotesAllowed"] = len(cont["ContestSelection"])
    return cont

makeContestMechanic(
    "approval",
    {
        "@type": "ElectionResults.CandidateContest",
        "VoteVariation": "approval",
        #"VotesAllowed": 5, # TODO: for approval, number of choices
        "BallotSubTitle": "Vote for as many as you like",
        "NumberElected": 1,
    },
    approvalContestModf,
)

def nopContestModf(relec, cont):
    pass

makeContestMechanic(
    "plurality",
    {
        "@type": "ElectionResults.CandidateContest",
        "VoteVariation": "plurality",
        "VotesAllowed": 1,
        "BallotSubTitle": "Vote for one",
        "NumberElected": 1,
    },
    nopContestModf,
)

def ballotmeasureContestModf(relec, cont):
    cont["ContestSelection"] = relec.yesOrNoBallotMeasureSelections()
    cont["ConStatement"] = randomWordString(10)
    cont["ProStatement"] = randomWordString(10)
    #"EffectOfAbstain"?
    cont["SummaryText"] = randomWordString(20)
    cont["FullText"] = randomWordString(1000)

makeContestMechanic(
    "ballotmeasure",
    {
        "@type": "ElectionResults.BallotMeasureContest",
        #"VoteVariation": "plurality",
        #"VotesAllowed": 1,
        "BallotSubTitle": "Vote Yes or No",
        #"NumberElected": 1,
        "Type": "referendum",
        "InfoUri": "https://betterpolls.com/",
    },
    ballotmeasureContestModf,
)

# TODO: ElectionResults.RetentionContest
# TODO: ElectionResults.PartyContest

def setRandomMechanic(relec, contest):
    cm = random.choice(contestMechanics)
    contest.update(cm.data)
    cm.fn(relec, contest)

def bin(they, n):
    bins = []
    curbin = []
    # bresenham
    D = (2*n) - len(they)
    for i, v in enumerate(they):
        curbin.append(v)
        if D > 0:
            bins.append(curbin)
            curbin = []
            D -= 2*len(they)
        D += 2*n
    if curbin:
        bins.append(curbin)
    return bins


class RandElection:
    def __init__(self):
        self.numLeafGpUnits = 10
        self.numL2GpUnits = 2
        self.numParties = 3
        self.leafContests = 1
        self.l2Contests = 1
        self.topContests = 2
        self.candidatesPerContestMin = 3
        self.candidatesPerContestMax = 13

        typeSequences = Sequences()
        self.typeSequences = typeSequences
        self._party_id = typeSequences.sourceForType("ElectionResults.Party")
        self._person_id = typeSequences.sourceForType("ElectionResults.Person")
        self._candidate_id = typeSequences.sourceForType("ElectionResults.Candidate")
        self._csel_id = typeSequences.sourceForType("ElectionResults.CandidateSelection")
        self._bmsel_id = typeSequences.sourceForType("ElectionResults.BallotMeasureSelection")
        self._office_id = typeSequences.sourceForType("ElectionResults.Office")
        self._gpunit_id = typeSequences.sourceForType("ElectionResults.ReportingUnit")
        self._contest_id = typeSequences.sourceForType("ElectionResults.CandidateContest")
        self._bmcont_id = typeSequences.sourceForType("ElectionResults.BallotMeasureContest")
        self._header_id = typeSequences.sourceForType("ElectionResults.Header")
        #
        self.parties = []
        self.persons = []
        self.candidates = []
        self.offices = []
        self.gpunits = []
        self.contests = []
        self.headers = []

    def makeParty(self):
        party = {
            "@id": self._party_id(),
            "@type": "ElectionResults.Party",
            "Name": randomName(),
            "Slogan": randomWordString(5),
        }
        self.parties.append(party)
        return party

    def makePerson(self):
        person = {
            "@id": self._person_id(),
            "@type": "ElectionResults.Person",
            "FullName": randomName() + ' ' + randomName(),
            "PartyId": random.choice(self.parties)["@id"],
            "Profession": randomWordString(3),
        }
        self.persons.append(person)
        return person

    def makeCandidate(self):
        # person = random.choice(self.persons)
        person = self.makePerson()
        candidate = {
            #required
            "@id": self._candidate_id(),
            "@type": "ElectionResults.Candidate",
            "BallotName": person["FullName"],
            #etc
            "PersonId": person["@id"],
        }
        self.candidates.append(candidate)
        return candidate

    def makeOffice(self):
        office = {
            "@id": self._office_id(),
            "@type": "ElectionResults.Office",
            "Name": randomWordString(2),
            "Description": randomWordString(5),
        }
        self.offices.append(office)
        return office

    def makeGpUnit(self, gpunitSubs=None):
        gpunit = {
            "@id": self._gpunit_id(),
            "@type": "ElectionResults.ReportingUnit",
            "Type": "city",
            "Name": randomName(),
        }
        if gpunitSubs:
            gpunit["ComposingGpUnitIds"] = [x["@id"] for x in gpunitSubs]
        self.gpunits.append(gpunit)
        return gpunit

    def cselForCandidate(self, candidate):
        csel = {
            "@id": self._csel_id(),
            "@type": "ElectionResults.CandidateSelection",
            "CandidateIds": [candidate["@id"]],
        }
        return csel

    def yesOrNoBallotMeasureSelections(self):
        return [
            {
                "@id": self._bmsel_id(),
                "@type": "ElectionResults.BallotMeasureSelection",
                "Selection": "Yes",
                "SequenceOrder": 1,
            },
            {
                "@id": self._bmsel_id(),
                "@type": "ElectionResults.BallotMeasureSelection",
                "Selection": "No",
                "SequenceOrder": 2,
            },
        ]

    def nCandidates(self):
        return random.randint(self.candidatesPerContestMin, self.candidatesPerContestMax)

    def makeContest(self, gpunit, ncandidates=None):
        '''Make a Contest, its candidates, and office'''
        contest = {
            # required
            "@id": self._contest_id(),
            #"@type": "ElectionResults.CandidateContest",
            "Name": randomWordString(3),
            "ElectionDistrictId": gpunit["@id"],

            # # election mechanic group
            # "VoteVariation": "approval",
            # "VotesAllowed": 5, # TODO: for approval, number of choices
            # "BallotSubTitle": "Vote for as many as you like",
            # "NumberElected": 1,

            # other
            "BallotTitle": randomWordString(5),
            #"OfficeIds": [self.makeOffice()["@id"]],
        }
        setRandomMechanic(self, contest)
        if not contest.get("ContestSelection"):
            if ncandidates is None:
                ncandidates = random.randint(self.candidatesPerContestMin, self.candidatesPerContestMax)
            contest["ContestSelection"] = [self.cselForCandidate(self.makeCandidate()) for i in range(ncandidates)]
        if (contest["@type"] == "ElectionResults.CandidateContest") and not contest.get("OfficeIds"):
            contest["OfficeIds"] = [self.makeOffice()["@id"]]
        self.contests.append(contest)
        return contest

    def instructions(self):
        header = {
            "@id": self._header_id(),
            "@type": "ElectionResults.Header",
            "Name": "Instructions",
        }
        self.headers.append(header)
        return header

    def columnBreak(self):
        header = {
            "@id": self._header_id(),
            "@type": "ElectionResults.Header",
            "Name": "ColumnBreak",
        }
        self.headers.append(header)
        return header

    def buildElectionReport(self):
        for _ in range(self.numParties):
            self.makeParty()

        er = {
            # required fields
            "@type": "ElectionReport",
            "Format": "summary-contest",
            "GeneratedDate": time.strftime("%Y-%m-%d %H:%M:%S %z", time.localtime()),
            "Issuer": "bolson",
            "IssuerAbbreviation": "bolson",
            "SequenceStart": 1,
            "SequenceEnd": 1,
            "Status": "pre-election",
            "VendorApplicationId": "bolson's random fake election 0.0.1",

            "IsTest": True,
            "TestType": "pre-election,design",
        }
        leafGpUnits = [self.makeGpUnit() for _ in range(self.numLeafGpUnits)]
        l1groups = bin(leafGpUnits, self.numL2GpUnits)
        l2GpUnits = [self.makeGpUnit(l1g) for l1g in l1groups]
        topGpUnit = self.makeGpUnit(l2GpUnits)

        leafContests = [[self.makeContest(gpu) for _ in range(self.leafContests)] for gpu in leafGpUnits]
        l2Contests = [[self.makeContest(gpu) for _ in range(self.l2Contests)] for gpu in l2GpUnits]
        topContests = [self.makeContest(topGpUnit) for _ in range(self.topContests)]

        instructions = self.instructions()
        columnBreak = self.columnBreak()

        election = {
            # required
            "@type": "ElectionResults.Election",
            "Name":"Hypothetical Election",
            "Type": "special",
            "ElectionScopeId": topGpUnit["@id"],
            "StartDate": "2022-11-08",
            "EndDate": "2022-11-08",
        }
        bstyles = []
        for lgpu, lcont in zip(leafGpUnits, leafContests):
            l2gpu = None
            l2cont = None
            for tgpu, tcont in zip(l2GpUnits, l2Contests):
                if lgpu["@id"] in tgpu["ComposingGpUnitIds"]:
                    l2gpu = tgpu
                    l2cont = tcont
                    break
            oc = [
                {
                    "@type": "ElectionResults.OrderedHeader",
                    "HeaderId": instructions["@id"],
                },
                {
                    "@type": "ElectionResults.OrderedHeader",
                    "HeaderId": columnBreak["@id"],
                },
            ]
            for tcont in topContests:
                oc.append({
                    "@type": "ElectionResults.OrderedContest",
                    "ContestId": tcont["@id"],
                })
            for tcont in l2cont:
                oc.append({
                    "@type": "ElectionResults.OrderedContest",
                    "ContestId": tcont["@id"],
                })
            for tcont in lcont:
                oc.append({
                    "@type": "ElectionResults.OrderedContest",
                    "ContestId": tcont["@id"],
                })
            bstyles.append({
                "@type": "ElectionResults.BallotStyle",
                "GpUnitIds": [lgpu["@id"]],
                "OrderedContent": oc,
                "PageHeader": '''General Election, {DATE}
{PLACES} page {PAGE} of {PAGES}''',
            })
        election["BallotStyle"] = bstyles
        election["Candidate"] = self.candidates
        election["Contest"] = self.contests
        er["Election"] = [election]
        er["GpUnit"] = self.gpunits
        er["Header"] = self.headers
        er["Office"] = self.offices
        er["Party"] = self.parties
        er["Person"] = self.persons
        return er


def main():
    import argparse
    import json
    ap = argparse.ArgumentParser()
    ap.add_argument('--parties', type=int, default=3)
    ap.add_argument('--counties', type=int, default=2, help='level 2 geo-political units, bigger than a town, smaller than a state, e.g. counties')
    ap.add_argument('--county-contests', type=int, default=1, help='number of contests to run in each county/l2-gpunit')
    ap.add_argument('--towns', type=int, default=10, help='number of leaf geo-political units, towns/cities/municaplites/etc')
    ap.add_argument('--town-contests', type=int, default=1, help='number of contests to run in each town/leaf-gpunit')
    ap.add_argument('--top-contests', type=int, default=2, help='number of contests to run at the top level (state)')
    ap.add_argument('--cand-min', type=int, default=3, help='minimum number of candidates in a contest')
    ap.add_argument('--cand-max', type=int, default=9, help='maximum number of candidates in a contest')
    args = ap.parse_args()
    logging.basicConfig(level=logging.DEBUG)
    rer = RandElection()
    rer.numParties = args.parties
    rer.numL2GpUnits = args.counties
    rer.numLeafGpUnits = args.towns
    rer.leafContests = args.town_contests
    rer.l2Contests = args.county_contests
    rer.topContests = args.top_contests
    rer.candidatesPerContestMin = args.cand_min
    rer.candidatesPerContestMax = args.cand_max
    print(json.dumps(rer.buildElectionReport(), indent=2))

if __name__ == '__main__':
    main()
