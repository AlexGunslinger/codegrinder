FROM debian:jessie
MAINTAINER russ@russross.com

RUN apt-get update && \
    apt-get upgrade -y

RUN apt-get install -y --no-install-recommends \
    polyml \
    ledit \
    make

RUN mkdir /home/student && chmod 777 /home/student
USER 2000
WORKDIR /home/student
