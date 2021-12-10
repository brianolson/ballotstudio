#!/usr/bin/env python3

import logging
import random

logger = logging.getLogger(__name__)

blankContestFrac = 0.05

# ElectionRecord in
# returns map[contest @id]map[csel @id](bool marked)
def randVote(er):
    out = {}
    for el in er['Election']:
        #for bs in el['BallotStyle']:
        for co in el['Contest']:
            if random.random() < blankContestFrac:
                # leave blank
                continue
            va = co.get('VotesAllowed')
            if va is None:
                cotype = co['@type']
                if cotype == 'ElectionResults.BallotMeasureContest':
                    va = 1
                # TODO: RetentionContest, PartyContest
            if va > 1:
                getn = random.randint(1, va)
            else:
                getn = 1
            logger.debug('%s %s va %d getn %d', co['@id'], co['@type'], va, getn)
            chosen = random.sample(co['ContestSelection'], getn)
            out[co['@id']] = {x['@id']:True for x in chosen}
    return out

def main():
    import json
    import sys
    logging.basicConfig(level=logging.DEBUG)
    er = json.load(sys.stdin)
    json.dump(randVote(er), sys.stdout)

if __name__ == '__main__':
    main()
