FROM node:lts-alpine

WORKDIR /app
COPY ./script/neteaseapi/api_repo /app

RUN npm config set registry "https://registry.npmmirror.com/" \
    && npm install -g npm husky \
    && npm install --production

EXPOSE 3335
CMD PORT=3335 node app.js