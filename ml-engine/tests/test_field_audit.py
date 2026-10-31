"""Field-claim gates and paired diagnostic arithmetic; no model training or heavy dependencies."""
import copy
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import audit_field as A


def pair(reference='OP-1', ref_type='production_order', target='actual_qty'):
    row = dict(reference_type=ref_type, reference=reference, target=target, version='frozen-1',
               predicted=8.0, actual=10.0, stored_actual=10.0, predicted_at='2026-10-29T10:00:00Z',
               outcome_booked_at='2026-10-29T13:00:00Z', low=7.0, high=11.0, level=0.8)
    return [dict(row, model=A.MODEL), dict(row, model=A.BASELINE, predicted=9.0)]


def verified():
    return dict(origin='field_verified', source_ref='physical weighing log 123', verified_by='operator',
                held_out_from_training=True, outcome_observed_at='2026-10-29T12:00:00Z')


class FieldAuditTests(unittest.TestCase):
    def test_demo_cannot_become_field_even_with_good_errors(self):
        report = A.audit(pair(), {'production_order:OP-1': {'origin': 'demo_seed'}})
        self.assertEqual(report['status'], 'NO_VERIFIED_FIELD_PAIRS')
        self.assertEqual(report['field_metrics'], {})
        metric = report['demo_diagnostic_metrics_NOT_FIELD']['production/actual_qty']
        self.assertEqual(metric['model']['mae'], 2.0)
        self.assertEqual(metric['baseline']['mae'], 1.0)

    def test_db_origin_is_not_a_field_attestation(self):
        report = A.audit(pair(), {'production_order:OP-1': {'origin': 'db'}})
        self.assertEqual(report['field_metrics'], {})

    def test_metadata_and_training_independence_are_required(self):
        for missing in ('source_ref', 'verified_by', 'held_out_from_training'):
            origin = verified()
            origin.pop(missing)
            self.assertIsNotNone(A.field_exclusion(pair(), {'production_order:OP-1': origin}))

    def test_predictions_must_precede_physical_observation_not_just_booking(self):
        rows = pair()
        rows[0]['predicted_at'] = '2026-10-29T12:30:00Z'
        self.assertEqual(A.field_exclusion(rows, {'production_order:OP-1': verified()}), 'forecast_not_before_observation')
        rows[0]['predicted_at'] = '2026-10-29T10:00:00'
        self.assertEqual(A.field_exclusion(rows, {'production_order:OP-1': verified()}), 'invalid_timestamps')

    def test_verified_prospective_pairs_only_get_descriptive_metrics(self):
        report = A.audit(pair(), {'production_order:OP-1': verified()})
        self.assertEqual(report['status'], 'DESCRIPTIVE_FIELD_METRICS_ONLY')
        metric = report['field_metrics']['production/actual_qty']
        self.assertEqual(metric['independent_operations'], 1)
        self.assertEqual(metric['model']['wape_pct'], 20.0)
        self.assertEqual(metric['model_minus_baseline_mae'], 1.0)

    def test_six_part_rows_are_one_carcass(self):
        rows = [r for p in ('but', 'sirt', 'omuz', 'kaburga', 'kiyma', 'fire')
                for r in pair(ref_type='butchery_operation', target=p + '_kg')]
        report = A.audit(rows, {'butchery_operation:OP-1': {'origin': 'demo_seed'}})
        metric = report['demo_diagnostic_metrics_NOT_FIELD']['butchery/recorded_parts_kg']
        self.assertEqual(metric['rows'], 6)
        self.assertEqual(metric['independent_operations'], 1)

    def test_forecast_frozen_before_weighing_can_pass_the_field_gates(self):
        rows = [dict(r, forecast_source='butchery_forecast', forecast_input_kg=25.0, outcome_input_kg=25.0)
                for r in pair(ref_type='butchery_operation', target='but_kg')]
        report = A.audit(rows, {'butchery_operation:OP-1': verified()})
        self.assertEqual(report['status'], 'DESCRIPTIVE_FIELD_METRICS_ONLY')
        target = report['operations']['butchery_operation:OP-1']['targets'][0]
        self.assertEqual(target['forecast_source'], 'butchery_forecast')
        self.assertIsNone(target['field_exclusion'])

    def test_forecast_frozen_for_another_weight_is_not_scored(self):
        rows = [dict(r, forecast_source='butchery_forecast', forecast_input_kg=25.4, outcome_input_kg=25.0)
                for r in pair(ref_type='butchery_operation', target='but_kg')]
        report = A.audit(rows, {'butchery_operation:OP-1': verified()})
        self.assertEqual(report['invalid_pair_counts'], {'forecast_input_differs': 1})
        self.assertEqual(report['field_metrics'], {})

    def test_duplicate_versions_are_not_cherry_picked(self):
        rows = pair()
        rows.append(copy.deepcopy(rows[0]))
        with self.assertRaisesRegex(ValueError, 'duplicate forecast'):
            A.audit(rows, {})

    def test_stale_actual_and_mixed_versions_are_not_scored(self):
        rows = pair()
        rows[0]['stored_actual'] = 11.0
        report = A.audit(rows, {})
        self.assertEqual(report['invalid_pair_counts'], {'stale_stored_actual': 1})
        rows = pair() + pair(reference='OP-2')
        rows[2]['version'] = 'frozen-2'
        with self.assertRaisesRegex(ValueError, 'mixed model versions'):
            A.audit(rows, {})


if __name__ == '__main__':
    unittest.main()
