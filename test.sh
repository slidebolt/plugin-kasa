#!/bin/bash

# kasa-bundle test runner
# Usage: ./test.sh [discovery|toggle|persistence|all]

CMD="go test -v -tags=hardware ./private_tests/hardware_test.go ./private_tests/data_integrity_test.go ./private_tests/persistence_test.go"

case "$1" in
    "discovery")
        $CMD -run TestKasaDiscovery
        ;;
    "toggle")
        $CMD -run TestKasaToggleHardware
        ;;
    "persistence")
        $CMD -run TestKasaPersistence
        ;;
    "all")
        $CMD -run TestKasaDiscovery
        $CMD -run TestKasaToggleHardware
        $CMD -run TestKasaPersistence
        ;;
    *)
        echo "Usage: ./test.sh [discovery|toggle|persistence|all]"
        exit 1
        ;;
esac
