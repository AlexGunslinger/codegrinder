.SUFFIXES:
.SUFFIXES: .sml .xml

SMLSOURCE=$(wildcard *.sml)

all:	test

test:
	poly < tests/tests.sml

grade:
	poly < tests/tests.sml

run:	
	( cat $(SMLSOURCE) ; echo ';' ; cat ) | ledit poly

setup:
	sudo apt-get install -y polyml ledit make

clean:
	rm -f test_detail.xml
