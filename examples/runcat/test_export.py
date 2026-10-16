import datetime as dt
import json
from pathlib import Path
import tempfile
import unittest

import export


class SummaryPanelTests(unittest.TestCase):
    def setUp(self):
        self.now = dt.datetime(2026, 10, 16, tzinfo=export.UTC)
        self.time = self.now.isoformat()
        self.snapshot = {
            'project': 'demo', 'generated_at': self.time,
            'topology': {'checks': [], 'observers': [], 'applications': [], 'services': []},
            'checks': [], 'observers': [], 'incidents': [], 'services': [],
        }

    def render(self):
        panel = export.panel(self.snapshot, self.now)
        self.assertEqual(len(panel['metrics']), 5)
        for row in panel['metrics']:
            text = row['title'] + ': ' + row['formattedValue']
            self.assertLessEqual(export.width(text), 32, text)
            self.assertNotIn('\n', text)
        return panel, {r['title']: r for r in panel['metrics']}

    def test_bars_include_unknown_and_stale_members_in_denominator(self):
        for key in ['fresh', 'stale', 'missing']:
            self.snapshot['topology']['observers'].append({'id': key, 'stale_seconds': 30})
            self.snapshot['observers'].append({
                'id': key, 'health': 'up',
                'last_seen': self.time if key == 'fresh' else
                    (self.now - dt.timedelta(seconds=31)).isoformat() if key == 'stale' else None,
            })
            self.snapshot['topology']['applications'].append({'id': key})
            self.snapshot['topology']['services'].append({'id': key, 'application': key})
        self.snapshot['services'] = [{'id': 'fresh', 'health': 'up'}, {'id': 'stale', 'health': 'unknown'}]
        panel, rows = self.render()
        self.assertEqual(sum('normalizedValue' in r for r in panel['metrics']), 2)
        self.assertEqual(rows['Hosts']['normalizedValue'], 0.33333)
        self.assertEqual(rows['Apps']['normalizedValue'], 0.33333)

    def test_incidents_and_long_names_do_not_grow_summary(self):
        for i in range(100):
            key = str(i)
            self.snapshot['topology']['checks'].append({'id': key, 'name': '跨区域 Windows ' * 20})
            self.snapshot['incidents'].append({'checks': [key]})
            self.snapshot['topology']['applications'].append({'id': key, 'name': 'W' * 200})
        panel, rows = self.render()
        self.assertEqual(panel['metricsBarValue'], '▲ 100')
        self.assertEqual(rows['Apps']['normalizedValue'], 0)

    def test_expired_checks_are_unknown_and_paused_checks_stay_separate(self):
        self.snapshot['topology']['checks'] = [{'id': 'host', 'stale_seconds': 30}]
        self.snapshot['checks'] = [{
            'id': 'host', 'health': 'up', 'freshness': 'fresh',
            'last': {'sampled_at': (self.now - dt.timedelta(seconds=31)).isoformat()},
        }, {'id': 'paused', 'health': 'unknown', 'freshness': 'paused'}]
        panel, rows = self.render()
        self.assertEqual(rows['Unknown']['formattedValue'], '1 · 1 paused')
        self.assertEqual(rows['Checks']['formattedValue'], '0 up · 0 warn · 0 down')
        self.assertEqual(panel['metricsBarValue'], '○ Check')
        self.assertEqual(self.snapshot['checks'][0]['health'], 'up')

    def test_empty_fleet_does_not_claim_a_known_percentage(self):
        panel, _ = self.render()
        self.assertFalse(any('normalizedValue' in r for r in panel['metrics']))
        self.assertEqual(panel['metricsBarValue'], '○ —')

    def test_expired_snapshot_is_rejected(self):
        with self.assertRaises(ValueError):
            export.panel(self.snapshot, self.now + dt.timedelta(seconds=15))
        panel = export.unavailable()
        self.assertFalse(any('normalizedValue' in r for r in panel['metrics']))

    def test_project_guard(self):
        with self.assertRaises(ValueError):
            export.panel(self.snapshot, self.now, project='other')
        self.assertEqual(len(export.panel(self.snapshot, self.now, project='demo')['metrics']), 5)

    def test_output_replacement_keeps_complete_unicode_json(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'panel.json'
            output.write_text('old contents', encoding='utf-8')
            panel, _ = self.render()
            export.write(panel, output)
            self.assertEqual(json.loads(output.read_text(encoding='utf-8')), panel)
            self.assertEqual(list(Path(directory).iterdir()), [output])


if __name__ == '__main__':
    unittest.main()
