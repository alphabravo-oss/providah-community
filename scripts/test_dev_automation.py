import unittest
from dev_automation import replace_catalog


class CatalogTest(unittest.TestCase):
    def test_preserves_unrelated_entries_and_settings(self):
        custom = {'runtime': 'terraform', 'image': 'custom', 'version': '1'}
        old = {'runtime': 'opentofu', 'image': 'old', 'version': '1'}
        new = {'runtime': 'opentofu', 'image': 'new', 'version': '2'}
        import json
        lines = ['SECRET=unchanged', "AUTOMATION_RUNTIMES='" + json.dumps([custom, old]) + "'"]
        replaced = replace_catalog(lines, [old], [new])
        self.assertEqual(replaced[0], lines[0])
        self.assertEqual(json.loads(replaced[-1].split('=', 1)[1][1:-1]), [custom, new])
        self.assertEqual(replace_catalog(replaced, [new], [new]), replaced)
        stopped = replace_catalog(replaced, [new], [])
        self.assertEqual(json.loads(stopped[-1].split('=', 1)[1][1:-1]), [custom])
        with self.assertRaises(ValueError):
            replace_catalog(lines + [lines[-1]], [], [])

        with self.assertRaises(ValueError):
            replace_catalog(["AUTOMATION_RUNTIMES='" + json.dumps([{**new, 'dependency_hosts': ['example.com']}]) + "'"], [], [new])
        with self.assertRaises(ValueError):
            replace_catalog([], [], [{'runtime': "bad'quote", 'image': 'x', 'version': '1'}])
