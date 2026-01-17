#!/usr/bin/env python3
"""Test script for stockyard integration tests."""

import sys

def main():
    print("Test script running")
    print(f"Python version: {sys.version}")
    with open("/workspace/test-marker.txt", "w") as f:
        f.write("test completed\n")
    print("Test completed successfully")

if __name__ == "__main__":
    main()
