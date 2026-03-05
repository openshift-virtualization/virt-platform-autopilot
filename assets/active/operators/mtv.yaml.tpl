apiVersion: forklift.konveyor.io/v1beta1
kind: ForkliftController
metadata:
  name: forklift-controller
  namespace: openshift-mtv
spec:
  feature_ui: true
  feature_validation: true
  feature_volume_populator: true
