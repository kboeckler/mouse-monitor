package main

import (
	"testing"
)

func TestParseBatteryOutput_empty_error(t *testing.T) {
	_, err := parseBatteryOutput("")
	if err == nil {
		t.Errorf("Expected an error but had none")
	}
}

func TestParseBatteryOutput_someText_error(t *testing.T) {
	_, err := parseBatteryOutput("Testerror")
	if err == nil {
		t.Errorf("Expected an error but had none")
	}
}

func TestParseBatteryOutput_discharging_butNoFollowPercentage_error(t *testing.T) {
	_, err := parseBatteryOutput("Discharging [===   ] 42")
	if err == nil {
		t.Errorf("Expected an error but had none")
	}
}

func TestParseBatteryOutput_discharging_response(t *testing.T) {
	res, err := parseBatteryOutput("Discharging [===   ] 42 %")
	if err != nil {
		t.Errorf("Expected no error but had: %v", err)
	}
	if res.charging {
		t.Errorf("Expected result not to be charging but was %v", res.charging)
	}
	if res.level != 42 {
		t.Errorf("Expected result to have level=42 but was %v", res.level)
	}
}

func TestParseBatteryOutput_charging_response(t *testing.T) {
	res, err := parseBatteryOutput("Charging [===   ] 42 %")
	if err != nil {
		t.Errorf("Expected no error but had: %v", err)
	}
	if !res.charging {
		t.Errorf("Expected result to be charging but was %v", res.charging)
	}
	if res.level != 42 {
		t.Errorf("Expected result to have level=42 but was %v", res.level)
	}
}

