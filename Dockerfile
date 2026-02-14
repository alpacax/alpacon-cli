FROM alpine:latest
COPY alpacon /usr/local/bin/alpacon
ENTRYPOINT ["alpacon"]
CMD [ "version" ]
